package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) SpecialPackRightUsed(ctx context.Context, playerID, weekKey string) (bool, error) {
	var used int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM player_flags WHERE player_id=? AND namespace='special_pack' AND flag_key=? AND flag_value<>0`, playerID, "unpack_right:"+weekKey).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
