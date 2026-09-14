package store

import (
	"context"
	"time"
)

type LevelHistory struct {
	Level       int
	LeveledUpAt time.Time
}

func (s *Store) LevelHistories(ctx context.Context, playerID string) ([]LevelHistory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT player_level,leveled_up_at FROM player_level_histories WHERE player_id=? ORDER BY player_level`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []LevelHistory
	for rows.Next() {
		var value LevelHistory
		var at int64
		if err := rows.Scan(&value.Level, &at); err != nil {
			return nil, err
		}
		value.LeveledUpAt = time.Unix(at, 0).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}
