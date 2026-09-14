package store

import (
	"context"
	"fmt"
)

func (s *Store) SyncAccountLinks(ctx context.Context, playerID string, linkTypes []int32) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_account_links WHERE player_id=?`, playerID); err != nil {
		return err
	}
	now := s.now().UTC().Unix()
	seen := make(map[int32]bool, len(linkTypes))
	for _, linkType := range linkTypes {
		if linkType < 1 || linkType > 3 || seen[linkType] {
			continue
		}
		seen[linkType] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_account_links(player_id,link_type,linked_at) VALUES(?,?,?)`, playerID, linkType, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UnlinkAccount(ctx context.Context, playerID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM player_account_links WHERE player_id=?`, playerID)
	if err != nil {
		return err
	}
	if _, err := s.Player(ctx, playerID); err != nil {
		return fmt.Errorf("unlink player account: %w", err)
	}
	_, _ = result.RowsAffected()
	return nil
}
