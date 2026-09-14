package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	tradePowerLimit             = int64(5)
	tradePowerPeriod            = 24 * time.Hour
	tradePowerChargerReduction  = time.Hour
	tradePowerPokeGoldReduction = 4 * time.Hour
)

type TradeState struct {
	Power           int64
	PowerUpdatedAt  time.Time
	BlockAll        bool
	MessageStanceID string
	MessageLanguage int32
}

func (s *Store) HealTradePower(ctx context.Context, playerID, chargerID string, chargerAmount, pokeGoldAmount int64) (TradeState, error) {
	if chargerAmount < 0 || pokeGoldAmount < 0 || chargerAmount+pokeGoldAmount == 0 || pokeGoldAmount > 8 {
		return TradeState{}, fmt.Errorf("%w: invalid trade power heal", ErrRuleViolation)
	}
	if _, err := s.TradeState(ctx, playerID); err != nil {
		return TradeState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TradeState{}, err
	}
	defer tx.Rollback()
	state, err := scanTradeState(tx.QueryRowContext(ctx, `SELECT power,power_updated_at,block_all,message_stance_id,message_language FROM player_trade_state WHERE player_id=?`, playerID))
	if err != nil {
		return TradeState{}, err
	}
	now := s.now().UTC()
	state.Power, state.PowerUpdatedAt = regeneratedPower(state.Power, state.PowerUpdatedAt, now, tradePowerLimit, tradePowerPeriod)
	if state.Power >= tradePowerLimit {
		return TradeState{}, fmt.Errorf("%w: trade power is already full", ErrRuleViolation)
	}
	if chargerAmount > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE player_items SET quantity=quantity-? WHERE player_id=? AND item_kind='trade_power_charger' AND item_id=? AND quantity>=?`, chargerAmount, playerID, chargerID, chargerAmount)
		if err != nil {
			return TradeState{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return TradeState{}, fmt.Errorf("%w: trade power chargers", ErrInsufficientInventory)
		}
	}
	if pokeGoldAmount > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE player_pack_state SET poke_gold=poke_gold-? WHERE player_id=? AND poke_gold>=?`, pokeGoldAmount, playerID, pokeGoldAmount)
		if err != nil {
			return TradeState{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return TradeState{}, fmt.Errorf("%w: poke gold", ErrInsufficientInventory)
		}
	}
	reduction := time.Duration(chargerAmount)*tradePowerChargerReduction + time.Duration(pokeGoldAmount)*tradePowerPokeGoldReduction
	state.PowerUpdatedAt = state.PowerUpdatedAt.Add(-reduction)
	state.Power, state.PowerUpdatedAt = regeneratedPower(state.Power, state.PowerUpdatedAt, now, tradePowerLimit, tradePowerPeriod)
	if _, err := tx.ExecContext(ctx, `UPDATE player_trade_state SET power=?,power_updated_at=? WHERE player_id=?`, state.Power, state.PowerUpdatedAt.Unix(), playerID); err != nil {
		return TradeState{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_items WHERE player_id=? AND item_kind='trade_power_charger' AND quantity=0`, playerID); err != nil {
		return TradeState{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now.Unix(), playerID); err != nil {
		return TradeState{}, err
	}
	return state, tx.Commit()
}

func (s *Store) TradeState(ctx context.Context, playerID string) (TradeState, error) {
	now := s.now().UTC()
	state, err := scanTradeState(s.db.QueryRowContext(ctx, `SELECT power,power_updated_at,block_all,message_stance_id,message_language FROM player_trade_state WHERE player_id=?`, playerID))
	if errors.Is(err, sql.ErrNoRows) {
		state = TradeState{Power: tradePowerLimit, PowerUpdatedAt: now, MessageStanceID: "STANCE_ID_DESIRED_ONLY", MessageLanguage: 4}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO player_trade_state(player_id,power,power_updated_at,block_all,message_stance_id,message_language) VALUES(?,?,?,?,?,?)`, playerID, state.Power, now.Unix(), 0, state.MessageStanceID, state.MessageLanguage); err != nil {
			return TradeState{}, fmt.Errorf("initialize trade state: %w", err)
		}
	} else if err != nil {
		return TradeState{}, fmt.Errorf("load trade state: %w", err)
	}
	state.Power, state.PowerUpdatedAt = regeneratedPower(state.Power, state.PowerUpdatedAt, now, tradePowerLimit, tradePowerPeriod)
	if _, err := s.db.ExecContext(ctx, `UPDATE player_trade_state SET power=?,power_updated_at=? WHERE player_id=?`, state.Power, state.PowerUpdatedAt.Unix(), playerID); err != nil {
		return TradeState{}, fmt.Errorf("persist regenerated trade power: %w", err)
	}
	return state, nil
}

func (s *Store) SaveTradeSettings(ctx context.Context, playerID string, blockAll bool) (TradeState, error) {
	state, err := s.TradeState(ctx, playerID)
	if err != nil {
		return TradeState{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE player_trade_state SET block_all=? WHERE player_id=?`, blockAll, playerID); err != nil {
		return TradeState{}, err
	}
	state.BlockAll = blockAll
	return state, nil
}

func (s *Store) SaveTradeMessage(ctx context.Context, playerID, stanceID string, language int32) (TradeState, error) {
	state, err := s.TradeState(ctx, playerID)
	if err != nil {
		return TradeState{}, err
	}
	if stanceID == "" {
		return TradeState{}, fmt.Errorf("%w: trade message stance is required", ErrRuleViolation)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE player_trade_state SET message_stance_id=?,message_language=? WHERE player_id=?`, stanceID, language, playerID); err != nil {
		return TradeState{}, err
	}
	state.MessageStanceID = stanceID
	state.MessageLanguage = language
	return state, nil
}

type tradeStateScanner interface {
	Scan(...any) error
}

func scanTradeState(scanner tradeStateScanner) (TradeState, error) {
	var state TradeState
	var updatedAt int64
	var blockAll int
	if err := scanner.Scan(&state.Power, &updatedAt, &blockAll, &state.MessageStanceID, &state.MessageLanguage); err != nil {
		return TradeState{}, err
	}
	state.PowerUpdatedAt = time.Unix(updatedAt, 0).UTC()
	state.BlockAll = blockAll != 0
	return state, nil
}
