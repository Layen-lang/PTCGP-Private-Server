package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	ChallengePowerLimit     = int64(5)
	ChallengePowerPeriod    = 12 * time.Hour
	FeedChallengeExperience = int64(15)
	FeedLifetime            = 4 * time.Hour
)

type FeedEntry struct {
	ID, OwnerID, PackID string
	Cards               []string
	CreatedAt           time.Time
	Snooping            bool
	ChallengedCardID    string
}

type ChallengeState struct {
	Power     int64
	UpdatedAt time.Time
}

func (s *Store) FeedEntries(ctx context.Context, viewerID string, limit int) ([]FeedEntry, error) {
	if limit <= 0 || limit > 16 {
		limit = 16
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.entry_id,f.player_id,o.pack_id,f.created_at,fs.feed_id IS NOT NULL,COALESCE(fc.card_id,'') FROM feed_entries f JOIN pack_openings o ON o.opening_id=f.opening_id LEFT JOIN feed_snoops fs ON fs.player_id=? AND fs.feed_id=f.entry_id LEFT JOIN feed_challenges fc ON fc.player_id=? AND fc.feed_id=f.entry_id WHERE f.player_id<>? AND f.created_at>? ORDER BY f.created_at DESC LIMIT ?`, viewerID, viewerID, viewerID, s.now().UTC().Add(-FeedLifetime).Unix(), limit)
	if err != nil {
		return nil, err
	}
	var result []FeedEntry
	for rows.Next() {
		var value FeedEntry
		var created int64
		if err := rows.Scan(&value.ID, &value.OwnerID, &value.PackID, &created, &value.Snooping, &value.ChallengedCardID); err != nil {
			return nil, err
		}
		value.CreatedAt = time.Unix(created, 0).UTC()
		result = append(result, value)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		cardRows, err := s.db.QueryContext(ctx, `SELECT card_id FROM pack_opening_cards WHERE opening_id=(SELECT opening_id FROM feed_entries WHERE entry_id=?) AND pack_index=0 ORDER BY slot_index`, result[index].ID)
		if err != nil {
			return nil, err
		}
		for cardRows.Next() {
			var cardID string
			if err := cardRows.Scan(&cardID); err != nil {
				cardRows.Close()
				return nil, err
			}
			result[index].Cards = append(result[index].Cards, cardID)
		}
		if err := cardRows.Close(); err != nil {
			return nil, err
		}
	}
	filtered := result[:0]
	for _, value := range result {
		if len(value.Cards) > 0 {
			filtered = append(filtered, value)
		}
	}
	return filtered, nil
}

// BeginFeedSnoop reserves challenge power for the feed until its challenge is committed.
func (s *Store) BeginFeedSnoop(ctx context.Context, playerID, feedID string, cost int64) (ChallengeState, bool, error) {
	if playerID == "" || feedID == "" || cost <= 0 || cost > ChallengePowerLimit {
		return ChallengeState{}, false, fmt.Errorf("%w: invalid feed snoop", ErrRuleViolation)
	}
	if _, err := s.ChallengeState(ctx, playerID); err != nil {
		return ChallengeState{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeState{}, false, err
	}
	defer tx.Rollback()

	var reservedFeedID string
	err = tx.QueryRowContext(ctx, `SELECT feed_id FROM feed_snoops WHERE player_id=?`, playerID).Scan(&reservedFeedID)
	if err == nil {
		state, stateErr := challengeStateTx(ctx, tx, playerID, s.now().UTC())
		if reservedFeedID != feedID {
			return ChallengeState{}, false, fmt.Errorf("%w: another feed snoop is active", ErrRuleViolation)
		}
		return state, true, stateErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ChallengeState{}, false, err
	}

	var available int
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM feed_entries f WHERE f.entry_id=? AND f.player_id<>? AND f.created_at>? AND NOT EXISTS(SELECT 1 FROM feed_challenges fc WHERE fc.player_id=? AND fc.feed_id=f.entry_id))`, feedID, playerID, s.now().UTC().Add(-FeedLifetime).Unix(), playerID).Scan(&available)
	if err != nil {
		return ChallengeState{}, false, err
	}
	if available == 0 {
		return ChallengeState{}, false, fmt.Errorf("%w: feed is unavailable", ErrRuleViolation)
	}

	now := s.now().UTC()
	state, err := challengeStateTx(ctx, tx, playerID, now)
	if err != nil {
		return ChallengeState{}, false, err
	}
	if state.Power < cost {
		return ChallengeState{}, false, fmt.Errorf("%w: challenge power", ErrInsufficientInventory)
	}
	state.Power -= cost
	if _, err := tx.ExecContext(ctx, `UPDATE player_challenge_state SET power=?,power_updated_at=? WHERE player_id=?`, state.Power, state.UpdatedAt.Unix(), playerID); err != nil {
		return ChallengeState{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO feed_snoops(player_id,feed_id,cost,created_at) VALUES(?,?,?,?)`, playerID, feedID, cost, now.Unix()); err != nil {
		return ChallengeState{}, false, err
	}
	return state, false, tx.Commit()
}

func (s *Store) FeedEntry(ctx context.Context, feedID string) (FeedEntry, error) {
	var value FeedEntry
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT f.entry_id,f.player_id,o.pack_id,f.created_at FROM feed_entries f JOIN pack_openings o ON o.opening_id=f.opening_id WHERE f.entry_id=?`, feedID).Scan(&value.ID, &value.OwnerID, &value.PackID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return FeedEntry{}, ErrNotFound
	}
	if err != nil {
		return FeedEntry{}, err
	}
	value.CreatedAt = time.Unix(created, 0).UTC()
	rows, err := s.db.QueryContext(ctx, `SELECT card_id FROM pack_opening_cards WHERE opening_id=(SELECT opening_id FROM feed_entries WHERE entry_id=?) AND pack_index=0 ORDER BY slot_index`, feedID)
	if err != nil {
		return FeedEntry{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var cardID string
		if err := rows.Scan(&cardID); err != nil {
			return FeedEntry{}, err
		}
		value.Cards = append(value.Cards, cardID)
	}
	return value, rows.Err()
}

func (s *Store) ChallengeState(ctx context.Context, playerID string) (ChallengeState, error) {
	now := s.now().UTC()
	var state ChallengeState
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT power,power_updated_at FROM player_challenge_state WHERE player_id=?`, playerID).Scan(&state.Power, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		state = ChallengeState{Power: ChallengePowerLimit, UpdatedAt: now}
		_, err = s.db.ExecContext(ctx, `INSERT INTO player_challenge_state(player_id,power,power_updated_at) VALUES(?,?,?)`, playerID, state.Power, now.Unix())
		return state, err
	}
	if err != nil {
		return ChallengeState{}, err
	}
	state.UpdatedAt = time.Unix(updated, 0).UTC()
	state.Power, state.UpdatedAt = regeneratedPower(state.Power, state.UpdatedAt, now, ChallengePowerLimit, ChallengePowerPeriod)
	_, err = s.db.ExecContext(ctx, `UPDATE player_challenge_state SET power=?,power_updated_at=? WHERE player_id=?`, state.Power, state.UpdatedAt.Unix(), playerID)
	return state, err
}

func (s *Store) CommitFeedChallenge(ctx context.Context, playerID, feedID, transactionID, cardID string, cost int64) (ChallengeState, bool, error) {
	if transactionID == "" || cardID == "" || cost < 0 {
		return ChallengeState{}, false, fmt.Errorf("%w: invalid feed challenge", ErrRuleViolation)
	}
	if _, err := s.ChallengeState(ctx, playerID); err != nil {
		return ChallengeState{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeState{}, false, err
	}
	defer tx.Rollback()
	var existingCard string
	err = tx.QueryRowContext(ctx, `SELECT card_id FROM feed_challenges WHERE player_id=? AND (feed_id=? OR transaction_id=?)`, playerID, feedID, transactionID).Scan(&existingCard)
	if err == nil {
		state, stateErr := challengeStateTx(ctx, tx, playerID, s.now().UTC())
		return state, true, stateErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ChallengeState{}, false, err
	}
	now := s.now().UTC()
	state, err := challengeStateTx(ctx, tx, playerID, now)
	if err != nil {
		return ChallengeState{}, false, err
	}
	chargedCost := cost
	var reservedCost int64
	err = tx.QueryRowContext(ctx, `SELECT cost FROM feed_snoops WHERE player_id=? AND feed_id=?`, playerID, feedID).Scan(&reservedCost)
	if err == nil {
		chargedCost = 0
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ChallengeState{}, false, err
	}
	if state.Power < chargedCost {
		return ChallengeState{}, false, fmt.Errorf("%w: challenge power", ErrInsufficientInventory)
	}
	state.Power -= chargedCost
	if _, err := tx.ExecContext(ctx, `UPDATE player_challenge_state SET power=?,power_updated_at=? WHERE player_id=?`, state.Power, state.UpdatedAt.Unix(), playerID); err != nil {
		return ChallengeState{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO feed_challenges(player_id,feed_id,transaction_id,card_id,created_at) VALUES(?,?,?,?,?)`, playerID, feedID, transactionID, cardID, now.Unix()); err != nil {
		return ChallengeState{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM feed_snoops WHERE player_id=? AND feed_id=?`, playerID, feedID); err != nil {
		return ChallengeState{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+1,last_received_at=excluded.last_received_at`, playerID, cardID, 1, now.Unix(), now.Unix()); err != nil {
		return ChallengeState{}, false, err
	}
	if err := addCardInAccountLanguage(ctx, tx, playerID, cardID, 1); err != nil {
		return ChallengeState{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET experience=experience+?,state_version=state_version+1,updated_at=? WHERE player_id=?`, FeedChallengeExperience, now.Unix(), playerID); err != nil {
		return ChallengeState{}, false, err
	}
	return state, false, tx.Commit()
}

func challengeStateTx(ctx context.Context, tx *sql.Tx, playerID string, now time.Time) (ChallengeState, error) {
	var state ChallengeState
	var updated int64
	if err := tx.QueryRowContext(ctx, `SELECT power,power_updated_at FROM player_challenge_state WHERE player_id=?`, playerID).Scan(&state.Power, &updated); err != nil {
		return ChallengeState{}, err
	}
	state.UpdatedAt = time.Unix(updated, 0).UTC()
	state.Power, state.UpdatedAt = regeneratedPower(state.Power, state.UpdatedAt, now, ChallengePowerLimit, ChallengePowerPeriod)
	return state, nil
}

func (s *Store) HealChallengePower(ctx context.Context, playerID, chargerID string, chargerAmount, pokeGoldAmount int64) (ChallengeState, error) {
	if chargerAmount < 0 || pokeGoldAmount < 0 || chargerAmount+pokeGoldAmount == 0 || pokeGoldAmount > 8 {
		return ChallengeState{}, fmt.Errorf("%w: invalid challenge power heal", ErrRuleViolation)
	}
	if _, err := s.ChallengeState(ctx, playerID); err != nil {
		return ChallengeState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeState{}, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	state, err := challengeStateTx(ctx, tx, playerID, now)
	if err != nil {
		return ChallengeState{}, err
	}
	if state.Power >= ChallengePowerLimit {
		return ChallengeState{}, fmt.Errorf("%w: challenge power is already full", ErrRuleViolation)
	}
	if chargerAmount > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE player_items SET quantity=quantity-? WHERE player_id=? AND item_kind='challenge_power_charger' AND item_id=? AND quantity>=?`, chargerAmount, playerID, chargerID, chargerAmount)
		if err != nil {
			return ChallengeState{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ChallengeState{}, ErrInsufficientInventory
		}
	}
	if pokeGoldAmount > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE player_pack_state SET poke_gold=poke_gold-? WHERE player_id=? AND poke_gold>=?`, pokeGoldAmount, playerID, pokeGoldAmount)
		if err != nil {
			return ChallengeState{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ChallengeState{}, ErrInsufficientInventory
		}
	}
	state.UpdatedAt = state.UpdatedAt.Add(-(time.Duration(chargerAmount)*time.Hour + time.Duration(pokeGoldAmount)*2*time.Hour))
	state.Power, state.UpdatedAt = regeneratedPower(state.Power, state.UpdatedAt, now, ChallengePowerLimit, ChallengePowerPeriod)
	if _, err := tx.ExecContext(ctx, `UPDATE player_challenge_state SET power=?,power_updated_at=? WHERE player_id=?`, state.Power, state.UpdatedAt.Unix(), playerID); err != nil {
		return ChallengeState{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_items WHERE player_id=? AND item_kind='challenge_power_charger' AND quantity=0`, playerID); err != nil {
		return ChallengeState{}, err
	}
	return state, tx.Commit()
}
