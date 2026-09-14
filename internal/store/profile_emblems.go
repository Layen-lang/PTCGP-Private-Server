package store

import (
	"context"
	"fmt"
)

func (s *Store) SelectedEmblems(ctx context.Context, playerID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT emblem_id FROM player_selected_emblems WHERE player_id=? ORDER BY display_order`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s *Store) SetSelectedEmblems(ctx context.Context, playerID string, emblemIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_selected_emblems WHERE player_id=?`, playerID); err != nil {
		return err
	}
	for order, id := range emblemIDs {
		result, err := tx.ExecContext(ctx, `INSERT INTO player_selected_emblems(player_id,display_order,emblem_id) SELECT ?,?,? WHERE EXISTS(SELECT 1 FROM player_profile_decorations WHERE player_id=? AND decoration_id=? AND quantity>0)`, playerID, order, id, playerID, id)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return fmt.Errorf("%w: emblem %q is not owned", ErrRuleViolation, id)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, s.now().UTC().Unix(), playerID); err != nil {
		return err
	}
	return tx.Commit()
}
