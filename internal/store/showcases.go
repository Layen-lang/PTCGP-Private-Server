package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const ShowcaseLimit = 15

type Showcase struct {
	ID, DisplayOrder uint64
	PublicSetting    int32
	Payload          []byte
}

type ShowcaseOrder struct {
	ID, DisplayOrder uint64
}

func (s *Store) SaveShowcase(ctx context.Context, playerID, kind string, value Showcase) (Showcase, error) {
	if kind != "album" && kind != "mount" {
		return Showcase{}, fmt.Errorf("%w: invalid showcase kind", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Showcase{}, err
	}
	defer tx.Rollback()
	if value.ID == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM player_showcases WHERE player_id=? AND showcase_kind=?`, playerID, kind).Scan(&count); err != nil {
			return Showcase{}, err
		}
		if count >= ShowcaseLimit {
			return Showcase{}, fmt.Errorf("%w: showcase limit reached", ErrRuleViolation)
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(showcase_id),0)+1 FROM player_showcases WHERE player_id=? AND showcase_kind=?`, playerID, kind).Scan(&value.ID); err != nil {
			return Showcase{}, err
		}
	}
	now := s.now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_showcases(player_id,showcase_kind,showcase_id,display_order,public_setting,payload,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(player_id,showcase_kind,showcase_id) DO UPDATE SET display_order=excluded.display_order,public_setting=excluded.public_setting,payload=excluded.payload,updated_at=excluded.updated_at`, playerID, kind, value.ID, value.DisplayOrder, value.PublicSetting, value.Payload, now); err != nil {
		return Showcase{}, err
	}
	if err := tx.Commit(); err != nil {
		return Showcase{}, err
	}
	return value, nil
}

func (s *Store) Showcases(ctx context.Context, playerID, kind string) ([]Showcase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT showcase_id,display_order,public_setting,payload FROM player_showcases WHERE player_id=? AND showcase_kind=? ORDER BY display_order,showcase_id`, playerID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Showcase
	for rows.Next() {
		var value Showcase
		if err := rows.Scan(&value.ID, &value.DisplayOrder, &value.PublicSetting, &value.Payload); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) Showcase(ctx context.Context, playerID, kind string, id uint64) (Showcase, error) {
	var value Showcase
	err := s.db.QueryRowContext(ctx, `SELECT showcase_id,display_order,public_setting,payload FROM player_showcases WHERE player_id=? AND showcase_kind=? AND showcase_id=?`, playerID, kind, id).Scan(&value.ID, &value.DisplayOrder, &value.PublicSetting, &value.Payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Showcase{}, ErrNotFound
	}
	return value, err
}

func (s *Store) DeleteShowcase(ctx context.Context, playerID, kind string, id uint64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM player_showcases WHERE player_id=? AND showcase_kind=? AND showcase_id=?`, playerID, kind, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) OrderShowcases(ctx context.Context, playerID, kind string, orders []ShowcaseOrder) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := make(map[uint64]bool, len(orders))
	for _, order := range orders {
		if order.ID == 0 || seen[order.ID] {
			return fmt.Errorf("%w: duplicate or empty showcase order", ErrRuleViolation)
		}
		seen[order.ID] = true
		result, err := tx.ExecContext(ctx, `UPDATE player_showcases SET display_order=?,updated_at=? WHERE player_id=? AND showcase_kind=? AND showcase_id=?`, order.DisplayOrder, s.now().UTC().Unix(), playerID, kind, order.ID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrNotFound
		}
	}
	return tx.Commit()
}
