package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) SharePackOpening(ctx context.Context, playerID, transactionID string) error {
	var openingID string
	if err := s.db.QueryRowContext(ctx, `SELECT opening_id FROM pack_openings WHERE player_id=? AND transaction_id=?`, playerID, transactionID).Scan(&openingID); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: pack transaction", ErrNotFound)
	} else if err != nil {
		return err
	}
	entryID, err := randomUUID()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO feed_entries(entry_id,player_id,opening_id,created_at) VALUES(?,?,?,?)`, entryID, playerID, openingID, s.now().UTC().Unix())
	return err
}

func (s *Store) StartSoloBattle(ctx context.Context, playerID, battleID string) (string, error) {
	if battleID == "" {
		return "", fmt.Errorf("battle ID is required")
	}
	token, err := randomUUID()
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO solo_battle_runs(run_id,player_id,battle_id,started_at) VALUES(?,?,?,?)`, token, playerID, battleID, s.now().UTC().Unix())
	return token, err
}

func (s *Store) FinishSoloBattle(ctx context.Context, playerID, token string, result int32) (bool, error) {
	var finished sql.NullInt64
	var battleID string
	if err := s.db.QueryRowContext(ctx, `SELECT battle_id,finished_at FROM solo_battle_runs WHERE run_id=? AND player_id=?`, token, playerID).Scan(&battleID, &finished); errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: battle session", ErrNotFound)
	} else if err != nil {
		return false, err
	}
	if finished.Valid {
		return false, nil
	}
	var previous int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM solo_battle_runs WHERE player_id=? AND battle_id=? AND result=1 AND finished_at IS NOT NULL`, playerID, battleID).Scan(&previous); err != nil {
		return false, err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE solo_battle_runs SET finished_at=?,result=?,rewarded=1 WHERE run_id=?`, s.now().UTC().Unix(), result, token)
	return result == 1 && previous == 0, err
}

// SoloBattleClearStates returns the durable first-clear state for the requested
// battle IDs. Unknown or never-started battles are intentionally reported as
// uncleared so the response remains aligned with the request order.
func (s *Store) SoloBattleClearStates(ctx context.Context, playerID string, battleIDs []string) (map[string]bool, error) {
	states := make(map[string]bool, len(battleIDs))
	for _, battleID := range battleIDs {
		if battleID == "" {
			return nil, fmt.Errorf("%w: battle ID is required", ErrRuleViolation)
		}
		var cleared int
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM solo_battle_runs
			WHERE player_id=? AND battle_id=? AND result=1 AND finished_at IS NOT NULL
		)`, playerID, battleID).Scan(&cleared); err != nil {
			return nil, err
		}
		states[battleID] = cleared != 0
	}
	return states, nil
}

const (
	EventPowerLimit  = int64(5)
	EventPowerPeriod = 12 * time.Hour
)

type EventPowerState struct {
	EventID   string
	Power     int64
	UpdatedAt time.Time
}

func (s *Store) EventPower(ctx context.Context, playerID, eventID string) (EventPowerState, error) {
	if eventID == "" {
		return EventPowerState{}, fmt.Errorf("%w: event ID is required", ErrRuleViolation)
	}
	now := s.now().UTC()
	var state EventPowerState
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT event_id,power,power_updated_at FROM player_event_powers WHERE player_id=? AND event_id=?`, playerID, eventID).Scan(&state.EventID, &state.Power, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		state = EventPowerState{EventID: eventID, Power: EventPowerLimit, UpdatedAt: now}
		_, err = s.db.ExecContext(ctx, `INSERT INTO player_event_powers(player_id,event_id,power,power_updated_at) VALUES(?,?,?,?)`, playerID, eventID, state.Power, now.Unix())
		return state, err
	}
	if err != nil {
		return EventPowerState{}, err
	}
	state.UpdatedAt = time.Unix(updated, 0).UTC()
	state.Power, state.UpdatedAt = regeneratedPower(state.Power, state.UpdatedAt, now, EventPowerLimit, EventPowerPeriod)
	_, err = s.db.ExecContext(ctx, `UPDATE player_event_powers SET power=?,power_updated_at=? WHERE player_id=? AND event_id=?`, state.Power, state.UpdatedAt.Unix(), playerID, eventID)
	return state, err
}

func (s *Store) StartEventSoloBattle(ctx context.Context, playerID, eventID, battleID string, cost int64) (string, EventPowerState, error) {
	if cost <= 0 {
		cost = 1
	}
	if _, err := s.EventPower(ctx, playerID, eventID); err != nil {
		return "", EventPowerState{}, err
	}
	token, err := randomUUID()
	if err != nil {
		return "", EventPowerState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", EventPowerState{}, err
	}
	defer tx.Rollback()
	var state EventPowerState
	var updated int64
	if err := tx.QueryRowContext(ctx, `SELECT event_id,power,power_updated_at FROM player_event_powers WHERE player_id=? AND event_id=?`, playerID, eventID).Scan(&state.EventID, &state.Power, &updated); err != nil {
		return "", EventPowerState{}, err
	}
	now := s.now().UTC()
	state.UpdatedAt = time.Unix(updated, 0).UTC()
	state.Power, state.UpdatedAt = regeneratedPower(state.Power, state.UpdatedAt, now, EventPowerLimit, EventPowerPeriod)
	if state.Power < cost {
		return "", EventPowerState{}, ErrInsufficientInventory
	}
	state.Power -= cost
	if _, err := tx.ExecContext(ctx, `UPDATE player_event_powers SET power=?,power_updated_at=? WHERE player_id=? AND event_id=?`, state.Power, state.UpdatedAt.Unix(), playerID, eventID); err != nil {
		return "", EventPowerState{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO solo_battle_runs(run_id,player_id,battle_id,started_at) VALUES(?,?,?,?)`, token, playerID, battleID, now.Unix()); err != nil {
		return "", EventPowerState{}, err
	}
	return token, state, tx.Commit()
}

func (s *Store) HealEventPower(ctx context.Context, playerID, eventID, chargerID string, chargerAmount, pokeGoldAmount int64) (EventPowerState, error) {
	if chargerAmount < 0 || pokeGoldAmount < 0 || chargerAmount+pokeGoldAmount == 0 || pokeGoldAmount > 8 {
		return EventPowerState{}, fmt.Errorf("%w: invalid event power heal", ErrRuleViolation)
	}
	if _, err := s.EventPower(ctx, playerID, eventID); err != nil {
		return EventPowerState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EventPowerState{}, err
	}
	defer tx.Rollback()
	var state EventPowerState
	var updated int64
	if err := tx.QueryRowContext(ctx, `SELECT event_id,power,power_updated_at FROM player_event_powers WHERE player_id=? AND event_id=?`, playerID, eventID).Scan(&state.EventID, &state.Power, &updated); err != nil {
		return EventPowerState{}, err
	}
	now := s.now().UTC()
	state.UpdatedAt = time.Unix(updated, 0).UTC()
	state.Power, state.UpdatedAt = regeneratedPower(state.Power, state.UpdatedAt, now, EventPowerLimit, EventPowerPeriod)
	if state.Power >= EventPowerLimit {
		return EventPowerState{}, fmt.Errorf("%w: event power is already full", ErrRuleViolation)
	}
	if chargerAmount > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE player_items SET quantity=quantity-? WHERE player_id=? AND item_kind='event_power_charger' AND item_id=? AND quantity>=?`, chargerAmount, playerID, chargerID, chargerAmount)
		if err != nil {
			return EventPowerState{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return EventPowerState{}, ErrInsufficientInventory
		}
	}
	if pokeGoldAmount > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE player_pack_state SET poke_gold=poke_gold-? WHERE player_id=? AND poke_gold>=?`, pokeGoldAmount, playerID, pokeGoldAmount)
		if err != nil {
			return EventPowerState{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return EventPowerState{}, ErrInsufficientInventory
		}
	}
	state.UpdatedAt = state.UpdatedAt.Add(-(time.Duration(chargerAmount)*12*time.Hour + time.Duration(pokeGoldAmount)*12*time.Hour))
	state.Power, state.UpdatedAt = regeneratedPower(state.Power, state.UpdatedAt, now, EventPowerLimit, EventPowerPeriod)
	if _, err := tx.ExecContext(ctx, `UPDATE player_event_powers SET power=?,power_updated_at=? WHERE player_id=? AND event_id=?`, state.Power, state.UpdatedAt.Unix(), playerID, eventID); err != nil {
		return EventPowerState{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_items WHERE player_id=? AND item_kind='event_power_charger' AND quantity=0`, playerID); err != nil {
		return EventPowerState{}, err
	}
	return state, tx.Commit()
}
