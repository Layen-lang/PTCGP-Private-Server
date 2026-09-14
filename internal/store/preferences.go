package store

import (
	"context"
	"fmt"
	"time"
)

type Flag struct {
	Key       string
	Value     int64
	UpdatedAt time.Time
}

func (s *Store) Flags(ctx context.Context, playerID, namespace string, keys []string) (map[string]Flag, error) {
	result := make(map[string]Flag)
	rows, err := s.db.QueryContext(ctx, `SELECT flag_key,flag_value,updated_at FROM player_flags WHERE player_id=? AND namespace=?`, playerID, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	filter := make(map[string]bool, len(keys))
	for _, key := range keys {
		filter[key] = true
	}
	for rows.Next() {
		var flag Flag
		var updatedAt int64
		if err := rows.Scan(&flag.Key, &flag.Value, &updatedAt); err != nil {
			return nil, err
		}
		if len(filter) == 0 || filter[flag.Key] {
			flag.UpdatedAt = time.Unix(updatedAt, 0).UTC()
			result[flag.Key] = flag
		}
	}
	return result, rows.Err()
}

func (s *Store) SetFlags(ctx context.Context, playerID, namespace string, values map[string]int64) (time.Time, error) {
	if namespace == "" {
		return time.Time{}, fmt.Errorf("flag namespace is required")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	for key, value := range values {
		if key == "" {
			return time.Time{}, fmt.Errorf("flag key is required")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_flags(player_id,namespace,flag_key,flag_value,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,namespace,flag_key) DO UPDATE SET flag_value=excluded.flag_value,updated_at=excluded.updated_at`, playerID, namespace, key, value, now.Unix()); err != nil {
			return time.Time{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

func (s *Store) SavePlayerSettings(ctx context.Context, playerID, language, country string, useLastLogin, useLoginData, usePerformanceErrors bool) error {
	if language == "" || country == "" {
		return fmt.Errorf("language and country are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE player_settings SET language=?,country=? WHERE player_id=?`, language, country, playerID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrNotFound
	}
	now := s.now().UTC().Unix()
	values := map[string]bool{"use_last_login": useLastLogin, "use_login_data": useLoginData, "use_performance_errors": usePerformanceErrors}
	for key, enabled := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_flags(player_id,namespace,flag_key,flag_value,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,namespace,flag_key) DO UPDATE SET flag_value=excluded.flag_value,updated_at=excluded.updated_at`, playerID, "settings", key, enabled, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdatePlayerLocale changes only the language and region preferences.
func (s *Store) UpdatePlayerLocale(ctx context.Context, playerID, language, country string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE player_settings SET language=?,country=? WHERE player_id=?`, language, country, playerID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrNotFound
	}
	return nil
}
