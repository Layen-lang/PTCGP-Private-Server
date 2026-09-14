package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type CardExchangeOperation struct {
	CatalogID, CardID, ProductID, ProductKind string
	Amount, ConsumeCards, ConsumeBrightSand   int64
	ProductAmount, RequiredParentCount        int64
	ParentID                                  string
}

type CardExchangeCount struct {
	CatalogID string
	Count     int64
	UpdatedAt time.Time
}

func (s *Store) ExchangeCardCosmetics(ctx context.Context, playerID string, operations []CardExchangeOperation) ([]CardExchangeCount, error) {
	if len(operations) == 0 || len(operations) > 30 {
		return nil, fmt.Errorf("%w: between 1 and 30 exchanges required", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	var results []CardExchangeCount
	for _, operation := range operations {
		if operation.Amount <= 0 || operation.ProductAmount <= 0 || operation.ConsumeCards < 0 || operation.ConsumeBrightSand < 0 {
			return nil, fmt.Errorf("%w: invalid card exchange amount", ErrRuleViolation)
		}
		if operation.ParentID != "" && operation.RequiredParentCount > 0 {
			var parentCount int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(exchange_count,0) FROM card_exchange_counts WHERE player_id=? AND catalog_id=?`, playerID, operation.ParentID).Scan(&parentCount); err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			if parentCount < operation.RequiredParentCount {
				return nil, fmt.Errorf("%w: parent exchange is locked", ErrRuleViolation)
			}
		}
		cards := operation.ConsumeCards * operation.Amount
		if cards > 0 {
			result, err := tx.ExecContext(ctx, `UPDATE player_cards SET quantity=quantity-?,last_received_at=? WHERE player_id=? AND card_id=? AND quantity>=?`, cards, now.Unix(), playerID, operation.CardID, cards)
			if err != nil {
				return nil, err
			}
			if changed, _ := result.RowsAffected(); changed != 1 {
				return nil, fmt.Errorf("%w: not enough cards", ErrInsufficientInventory)
			}
		}
		sand := operation.ConsumeBrightSand * operation.Amount
		if sand > 0 {
			result, err := tx.ExecContext(ctx, `UPDATE player_currencies SET quantity=quantity-? WHERE player_id=? AND currency_id='SHINEDUST' AND quantity>=?`, sand, playerID, sand)
			if err != nil {
				return nil, err
			}
			if changed, _ := result.RowsAffected(); changed != 1 {
				return nil, fmt.Errorf("%w: not enough bright sand", ErrInsufficientInventory)
			}
		}
		productAmount := operation.ProductAmount * operation.Amount
		switch operation.ProductKind {
		case "card_skin":
			_, err = tx.ExecContext(ctx, `INSERT INTO player_card_skins(player_id,card_id,skin_id,quantity) VALUES(?,?,?,?) ON CONFLICT(player_id,card_id,skin_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, operation.CardID, operation.ProductID, productAmount)
		case "card_frame":
			_, err = tx.ExecContext(ctx, `INSERT INTO player_card_frames(player_id,card_id,frame_id,quantity) VALUES(?,?,?,?) ON CONFLICT(player_id,card_id,frame_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, operation.CardID, operation.ProductID, productAmount)
		case "currency":
			_, err = tx.ExecContext(ctx, `INSERT INTO player_currencies(player_id,currency_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,currency_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, operation.ProductID, productAmount)
		default:
			return nil, fmt.Errorf("%w: unsupported card exchange product", ErrRuleViolation)
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO card_exchange_counts(player_id,catalog_id,exchange_count,updated_at) VALUES(?,?,?,?) ON CONFLICT(player_id,catalog_id) DO UPDATE SET exchange_count=exchange_count+excluded.exchange_count,updated_at=excluded.updated_at`, playerID, operation.CatalogID, operation.Amount, now.Unix()); err != nil {
			return nil, err
		}
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT exchange_count FROM card_exchange_counts WHERE player_id=? AND catalog_id=?`, playerID, operation.CatalogID).Scan(&count); err != nil {
			return nil, err
		}
		results = append(results, CardExchangeCount{CatalogID: operation.CatalogID, Count: count, UpdatedAt: now})
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_cards WHERE player_id=? AND quantity=0`, playerID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_currencies WHERE player_id=? AND quantity=0`, playerID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now.Unix(), playerID); err != nil {
		return nil, err
	}
	return results, tx.Commit()
}

func (s *Store) CardExchangeCounts(ctx context.Context, playerID string) ([]CardExchangeCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT catalog_id,exchange_count,updated_at FROM card_exchange_counts WHERE player_id=? ORDER BY updated_at,catalog_id`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CardExchangeCount
	for rows.Next() {
		var value CardExchangeCount
		var updated int64
		if err := rows.Scan(&value.CatalogID, &value.Count, &updated); err != nil {
			return nil, err
		}
		value.UpdatedAt = time.Unix(updated, 0).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}
