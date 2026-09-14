package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const CollectionLikeCooldown = 24 * time.Hour

type CollectionCandidate struct {
	Player    Player
	Payload   []byte
	LikeCount uint64
}

func (s *Store) SaveBestCollection(ctx context.Context, playerID string, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty best collection", ErrRuleViolation)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO player_best_collections(player_id,payload,updated_at) VALUES(?,?,?) ON CONFLICT(player_id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, playerID, payload, s.now().UTC().Unix())
	return err
}

func (s *Store) BestCollection(ctx context.Context, playerID string) ([]byte, error) {
	var payload []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM player_best_collections WHERE player_id=?`, playerID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return payload, err
}

// LikeCollection records at most one like per player pair during a rolling
// 24-hour window. Replays return the original expiry without incrementing.
func (s *Store) LikeCollection(ctx context.Context, likerID, targetID string) (time.Time, bool, error) {
	if likerID == "" || targetID == "" || likerID == targetID {
		return time.Time{}, false, fmt.Errorf("%w: invalid collection like", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, false, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM players WHERE player_id=?`, targetID).Scan(&exists); err != nil {
		return time.Time{}, false, err
	}
	if exists == 0 {
		return time.Time{}, false, ErrNotFound
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM player_best_collections WHERE player_id=?`, targetID).Scan(&exists); err != nil {
		return time.Time{}, false, err
	}
	if exists == 0 {
		return time.Time{}, false, fmt.Errorf("%w: target has no published collection", ErrRuleViolation)
	}
	now := s.now().UTC()
	var expiresUnix int64
	err = tx.QueryRowContext(ctx, `SELECT expires_at FROM collection_likes WHERE liker_player_id=? AND target_player_id=? ORDER BY liked_at DESC LIMIT 1`, likerID, targetID).Scan(&expiresUnix)
	if err == nil && now.Before(time.Unix(expiresUnix, 0)) {
		expires := time.Unix(expiresUnix, 0).UTC()
		if err := tx.Commit(); err != nil {
			return time.Time{}, false, err
		}
		return expires, false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, err
	}
	expires := now.Add(CollectionLikeCooldown)
	if _, err := tx.ExecContext(ctx, `INSERT INTO collection_likes(liker_player_id,target_player_id,liked_at,expires_at) VALUES(?,?,?,?)`, likerID, targetID, now.Unix(), expires.Unix()); err != nil {
		return time.Time{}, false, err
	}
	return expires, true, tx.Commit()
}

func (s *Store) CollectionLikeExpiry(ctx context.Context, likerID, targetID string) (time.Time, error) {
	var expires int64
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM collection_likes WHERE liker_player_id=? AND target_player_id=? ORDER BY liked_at DESC LIMIT 1`, likerID, targetID).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	value := time.Unix(expires, 0).UTC()
	if !s.now().UTC().Before(value) {
		return time.Time{}, nil
	}
	return value, nil
}

func (s *Store) CollectionLikeCount(ctx context.Context, targetID string) (uint64, error) {
	var count uint64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM collection_likes WHERE target_player_id=?`, targetID).Scan(&count)
	return count, err
}

func (s *Store) CollectionCandidates(ctx context.Context, excludedPlayerID string) ([]CollectionCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.player_id,p.display_name,p.level,p.experience,p.state_version,p.created_at,p.updated_at,b.payload,count(l.target_player_id) FROM player_best_collections b JOIN players p ON p.player_id=b.player_id LEFT JOIN collection_likes l ON l.target_player_id=p.player_id WHERE p.player_id<>? GROUP BY p.player_id,b.payload ORDER BY count(l.target_player_id) DESC,b.updated_at DESC,p.player_id`, excludedPlayerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CollectionCandidate
	for rows.Next() {
		var value CollectionCandidate
		var createdAt, updatedAt int64
		if err := rows.Scan(&value.Player.ID, &value.Player.DisplayName, &value.Player.Level, &value.Player.Experience, &value.Player.StateVersion, &createdAt, &updatedAt, &value.Payload, &value.LikeCount); err != nil {
			return nil, err
		}
		value.Player.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.Player.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}
