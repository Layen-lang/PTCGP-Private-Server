package store

import (
	"context"
	"database/sql"
)

type TrophyState struct {
	ID   string
	Rank int32
}

type TrophyCompletion struct {
	TrophyState
	BrightSandRewards [4]int64
}

func (s *Store) TrophyStates(ctx context.Context, playerID string, ids []string) ([]TrophyState, error) {
	var result []TrophyState
	for _, id := range ids {
		var value TrophyState
		err := s.db.QueryRowContext(ctx, `SELECT trophy_id,trophy_rank FROM player_trophies WHERE player_id=? AND trophy_id=?`, playerID, id).Scan(&value.ID, &value.Rank)
		if err == sql.ErrNoRows {
			result = append(result, TrophyState{ID: id})
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Store) CompleteTrophies(ctx context.Context, playerID string, trophies []TrophyCompletion) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var brightSand int64
	now := s.now().UTC().Unix()
	for _, trophy := range trophies {
		var current int32
		err := tx.QueryRowContext(ctx, `SELECT trophy_rank FROM player_trophies WHERE player_id=? AND trophy_id=?`, playerID, trophy.ID).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if trophy.Rank <= current {
			continue
		}
		for rank := current + 1; rank <= trophy.Rank; rank++ {
			brightSand += trophy.BrightSandRewards[rank-1]
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_trophies(player_id,trophy_id,trophy_rank,updated_at) VALUES(?,?,?,?) ON CONFLICT(player_id,trophy_id) DO UPDATE SET trophy_rank=excluded.trophy_rank,updated_at=excluded.updated_at`, playerID, trophy.ID, trophy.Rank, now); err != nil {
			return 0, err
		}
	}
	if brightSand > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_currencies(player_id,currency_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,currency_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, "SHINEDUST", brightSand); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
			return 0, err
		}
	}
	return brightSand, tx.Commit()
}
