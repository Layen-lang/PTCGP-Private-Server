package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type TutorialGrant struct {
	PlayerID, TutorialID        string
	Step                        int64
	Rewards                     []ShopInventoryChange
	Experience                  int64
	CeilGroup, GuaranteeID      string
	CeilPoints, GuaranteePoints int64
	Flags                       map[string]int64
}

type TutorialGrantResult struct {
	Rewards []ShopInventoryChange
	Replay  bool
}

func (s *Store) CommitOneTimeInventoryGrant(ctx context.Context, playerID, namespace, key string, rewards []ShopInventoryChange) ([]ShopInventoryChange, bool, error) {
	if playerID == "" || namespace == "" || key == "" {
		return nil, false, fmt.Errorf("%w: invalid one-time grant", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM player_flags WHERE player_id=? AND namespace=? AND flag_key=? AND flag_value<>0`, playerID, namespace, key).Scan(&exists)
	if err == nil {
		return nil, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	now := s.now().UTC().Unix()
	applied := make([]ShopInventoryChange, 0, len(rewards))
	for _, reward := range rewards {
		owned, err := inventoryQuantityTx(ctx, tx, playerID, reward)
		if err != nil {
			return nil, false, err
		}
		if owned >= reward.Amount {
			continue
		}
		reward.Amount -= owned
		if err := changeShopInventory(ctx, tx, playerID, reward, 1, now); err != nil {
			return nil, false, err
		}
		applied = append(applied, reward)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_flags(player_id,namespace,flag_key,flag_value,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,namespace,flag_key) DO UPDATE SET flag_value=1,updated_at=excluded.updated_at`, playerID, namespace, key, 1, now); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return applied, false, nil
}

func inventoryQuantityTx(ctx context.Context, tx *sql.Tx, playerID string, reward ShopInventoryChange) (int64, error) {
	query := ""
	args := []any{playerID, reward.ID}
	switch reward.Kind {
	case "card":
		query = `SELECT quantity FROM player_cards WHERE player_id=? AND card_id=?`
	case "currency":
		query = `SELECT quantity FROM player_currencies WHERE player_id=? AND currency_id=?`
	case "item":
		query = `SELECT quantity FROM player_items WHERE player_id=? AND item_kind=? AND item_id=?`
		args = []any{playerID, reward.SubID, reward.ID}
	case "profile_decoration":
		query = `SELECT quantity FROM player_profile_decorations WHERE player_id=? AND decoration_id=?`
	default:
		return 0, fmt.Errorf("%w: unsupported one-time reward", ErrRuleViolation)
	}
	var quantity int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&quantity); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return quantity, nil
}

// CommitTutorialGrant applies progress and every associated reward in one
// transaction. The recorded tutorial step is the idempotency boundary.
func (s *Store) CommitTutorialGrant(ctx context.Context, input TutorialGrant) (TutorialGrantResult, error) {
	if input.PlayerID == "" || input.TutorialID == "" || input.Step < 0 {
		return TutorialGrantResult{}, fmt.Errorf("%w: invalid tutorial grant", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TutorialGrantResult{}, err
	}
	defer tx.Rollback()
	var granted int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM player_tutorial_grants WHERE player_id=? AND tutorial_id=? AND tutorial_step=?`, input.PlayerID, input.TutorialID, input.Step).Scan(&granted)
	if err == nil {
		return TutorialGrantResult{Replay: true}, tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TutorialGrantResult{}, err
	}
	now := s.now().UTC().Unix()
	for _, reward := range input.Rewards {
		if err := changeShopInventory(ctx, tx, input.PlayerID, reward, 1, now); err != nil {
			return TutorialGrantResult{}, err
		}
	}
	if input.CeilGroup != "" && input.CeilPoints > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_pack_ceil_points(player_id,group_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,group_id) DO UPDATE SET quantity=quantity+excluded.quantity`, input.PlayerID, input.CeilGroup, input.CeilPoints); err != nil {
			return TutorialGrantResult{}, err
		}
	}
	if input.GuaranteeID != "" && input.GuaranteePoints > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_pack_guarantee_points(player_id,guarantee_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,guarantee_id) DO UPDATE SET quantity=quantity+excluded.quantity`, input.PlayerID, input.GuaranteeID, input.GuaranteePoints); err != nil {
			return TutorialGrantResult{}, err
		}
	}
	for key, value := range input.Flags {
		if key == "" {
			return TutorialGrantResult{}, fmt.Errorf("%w: empty tutorial flag", ErrRuleViolation)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_flags(player_id,namespace,flag_key,flag_value,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,namespace,flag_key) DO UPDATE SET flag_value=excluded.flag_value,updated_at=excluded.updated_at`, input.PlayerID, "tutorial", key, value, now); err != nil {
			return TutorialGrantResult{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_tutorial_grants(player_id,tutorial_id,tutorial_step,granted_at) VALUES(?,?,?,?)`, input.PlayerID, input.TutorialID, input.Step, now); err != nil {
		return TutorialGrantResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tutorial_progress(player_id,tutorial_id,step,completed,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,tutorial_id) DO UPDATE SET step=excluded.step,completed=1,updated_at=excluded.updated_at WHERE tutorial_progress.step < excluded.step OR tutorial_progress.completed=0`, input.PlayerID, input.TutorialID, input.Step, true, now); err != nil {
		return TutorialGrantResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE players SET experience=experience+?,state_version=state_version+1,updated_at=? WHERE player_id=?`, input.Experience, now, input.PlayerID)
	if err != nil {
		return TutorialGrantResult{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return TutorialGrantResult{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return TutorialGrantResult{}, err
	}
	return TutorialGrantResult{Rewards: append([]ShopInventoryChange(nil), input.Rewards...)}, nil
}

func (s *Store) CommitLevelUp(ctx context.Context, playerID string, targetLevel int, rewards []ShopInventoryChange) (int, []ShopInventoryChange, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, false, err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT level FROM players WHERE player_id=?`, playerID).Scan(&current); err != nil {
		return 0, nil, false, err
	}
	if targetLevel <= current {
		return current, nil, true, tx.Commit()
	}
	now := s.now().UTC().Unix()
	for _, reward := range rewards {
		if err := changeShopInventory(ctx, tx, playerID, reward, 1, now); err != nil {
			return 0, nil, false, err
		}
	}
	for level := current + 1; level <= targetLevel; level++ {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO player_level_histories(player_id,player_level,leveled_up_at) VALUES(?,?,?)`, playerID, level, now); err != nil {
			return 0, nil, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET level=?,state_version=state_version+1,updated_at=? WHERE player_id=?`, targetLevel, now, playerID); err != nil {
		return 0, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, false, err
	}
	return current, append([]ShopInventoryChange(nil), rewards...), false, nil
}
