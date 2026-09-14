package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type RentalDeckState struct {
	ID         string
	UsedCount  int64
	ObtainedAt time.Time
}

func (s *Store) RentalDecks(ctx context.Context, playerID string) ([]RentalDeckState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rental_deck_id,used_count,obtained_at FROM player_rental_decks WHERE player_id=? ORDER BY obtained_at,rental_deck_id`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RentalDeckState
	for rows.Next() {
		var value RentalDeckState
		var obtainedAt int64
		if err := rows.Scan(&value.ID, &value.UsedCount, &obtainedAt); err != nil {
			return nil, err
		}
		value.ObtainedAt = time.Unix(obtainedAt, 0).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) UseRentalDeck(ctx context.Context, playerID, rentalDeckID string, useLimit int64) (int64, error) {
	if rentalDeckID == "" || useLimit <= 0 {
		return 0, fmt.Errorf("%w: invalid rental deck", ErrRuleViolation)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE player_rental_decks SET used_count=used_count+1 WHERE player_id=? AND rental_deck_id=? AND used_count<?`, playerID, rentalDeckID, useLimit)
	if err != nil {
		return 0, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return 0, fmt.Errorf("%w: rental deck unavailable or exhausted", ErrRuleViolation)
	}
	var used int64
	if err := s.db.QueryRowContext(ctx, `SELECT used_count FROM player_rental_decks WHERE player_id=? AND rental_deck_id=?`, playerID, rentalDeckID).Scan(&used); err != nil {
		return 0, err
	}
	return used, nil
}

type SoloBattleTryState struct {
	ID             string
	CurrentCount   int64
	RewardReceived bool
}

func (s *Store) SoloBattleTries(ctx context.Context, playerID, battleID string, tryIDs []string) ([]SoloBattleTryState, error) {
	result := make([]SoloBattleTryState, 0, len(tryIDs))
	for _, tryID := range tryIDs {
		value := SoloBattleTryState{ID: tryID}
		var received int
		err := s.db.QueryRowContext(ctx, `SELECT current_count,reward_received FROM solo_battle_try_progress WHERE player_id=? AND battle_id=? AND battle_try_id=?`, playerID, battleID, tryID).Scan(&value.CurrentCount, &received)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		value.RewardReceived = received != 0
		result = append(result, value)
	}
	return result, nil
}

func (s *Store) SaveSoloBattleTries(ctx context.Context, playerID, battleID string, values []SoloBattleTryState) ([]SoloBattleTryState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	for _, value := range values {
		if value.ID == "" || value.CurrentCount < 0 {
			return nil, fmt.Errorf("%w: invalid solo battle try", ErrRuleViolation)
		}
		received := 0
		if value.RewardReceived {
			received = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO solo_battle_try_progress(player_id,battle_id,battle_try_id,current_count,reward_received,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(player_id,battle_id,battle_try_id) DO UPDATE SET current_count=MAX(current_count,excluded.current_count),reward_received=MAX(reward_received,excluded.reward_received),updated_at=excluded.updated_at`, playerID, battleID, value.ID, value.CurrentCount, received, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.ID)
	}
	return s.SoloBattleTries(ctx, playerID, battleID, ids)
}

type CompletedMission struct {
	ID          string
	CompletedAt time.Time
}

type MissionClaim struct {
	ID      string
	Rewards []ShopInventoryChange
}

func (s *Store) CompletedMissions(ctx context.Context, playerID string, missionIDs []string) ([]CompletedMission, error) {
	result := make([]CompletedMission, 0, len(missionIDs))
	for _, missionID := range missionIDs {
		var completedAt int64
		err := s.db.QueryRowContext(ctx, `SELECT completed_at FROM player_missions WHERE player_id=? AND mission_id=? AND completed_at IS NOT NULL`, playerID, missionID).Scan(&completedAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, CompletedMission{ID: missionID, CompletedAt: time.Unix(completedAt, 0).UTC()})
	}
	return result, nil
}

func (s *Store) ClaimMissions(ctx context.Context, playerID string, claims []MissionClaim) ([]ShopInventoryChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	var applied []ShopInventoryChange
	for _, claim := range claims {
		if claim.ID == "" {
			return nil, fmt.Errorf("%w: mission ID is required", ErrRuleViolation)
		}
		var claimedAt sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT claimed_at FROM player_missions WHERE player_id=? AND mission_id=?`, playerID, claim.ID).Scan(&claimedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if claimedAt.Valid {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_missions(player_id,mission_id,completed_at,claimed_at) VALUES(?,?,?,?) ON CONFLICT(player_id,mission_id) DO UPDATE SET completed_at=COALESCE(completed_at,excluded.completed_at),claimed_at=excluded.claimed_at`, playerID, claim.ID, now, now); err != nil {
			return nil, err
		}
		for _, reward := range claim.Rewards {
			if err := changeShopInventory(ctx, tx, playerID, reward, 1, now); err != nil {
				return nil, err
			}
			applied = append(applied, reward)
		}
	}
	if len(applied) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
			return nil, err
		}
	}
	return applied, tx.Commit()
}

func (s *Store) MissionGroupStepStates(ctx context.Context, playerID string, stepIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(stepIDs))
	for _, stepID := range stepIDs {
		var claimed int
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM player_mission_group_steps WHERE player_id=? AND mission_group_reward_step_id=?)`, playerID, stepID).Scan(&claimed); err != nil {
			return nil, err
		}
		result[stepID] = claimed != 0
	}
	return result, nil
}

func (s *Store) ClaimMissionGroupSteps(ctx context.Context, playerID string, claims []MissionClaim) ([]ShopInventoryChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	var applied []ShopInventoryChange
	for _, claim := range claims {
		result, err := tx.ExecContext(ctx, `INSERT INTO player_mission_group_steps(player_id,mission_group_reward_step_id,claimed_at) VALUES(?,?,?) ON CONFLICT(player_id,mission_group_reward_step_id) DO NOTHING`, playerID, claim.ID, now)
		if err != nil {
			return nil, err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			continue
		}
		for _, reward := range claim.Rewards {
			if err := changeShopInventory(ctx, tx, playerID, reward, 1, now); err != nil {
				return nil, err
			}
			applied = append(applied, reward)
		}
	}
	if len(applied) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
			return nil, err
		}
	}
	return applied, tx.Commit()
}
