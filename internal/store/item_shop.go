package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type ShopInventoryChange struct {
	Kind, ID, SubID string
	Amount          int64
}

type ShopPurchase struct {
	ShopID, ProductID, TransactionID string
	Amount, MaxTotal                 int64
	Price                            ShopInventoryChange
	Rewards                          []ShopInventoryChange
}

func (s *Store) ShopPurchaseCount(ctx context.Context, playerID, shopID, productID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(sum(amount),0) FROM item_shop_transactions WHERE player_id=? AND shop_id=? AND product_id=?`, playerID, shopID, productID).Scan(&count)
	return count, err
}

func (s *Store) CommitShopPurchase(ctx context.Context, playerID string, purchase ShopPurchase) (int64, bool, error) {
	if purchase.TransactionID == "" || purchase.ShopID == "" || purchase.ProductID == "" || purchase.Amount <= 0 {
		return 0, false, fmt.Errorf("%w: invalid shop purchase", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var existingProduct string
	var existingAmount int64
	err = tx.QueryRowContext(ctx, `SELECT product_id,amount FROM item_shop_transactions WHERE player_id=? AND transaction_id=?`, playerID, purchase.TransactionID).Scan(&existingProduct, &existingAmount)
	if err == nil {
		if existingProduct != purchase.ProductID || existingAmount != purchase.Amount {
			return 0, false, fmt.Errorf("%w: transaction payload changed", ErrRuleViolation)
		}
		count, err := shopPurchaseCountTx(ctx, tx, playerID, purchase.ShopID, purchase.ProductID)
		return count, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	count, err := shopPurchaseCountTx(ctx, tx, playerID, purchase.ShopID, purchase.ProductID)
	if err != nil {
		return 0, false, err
	}
	if purchase.MaxTotal > 0 && count+purchase.Amount > purchase.MaxTotal {
		return 0, false, fmt.Errorf("%w: shop purchase limit", ErrRuleViolation)
	}
	price := purchase.Price
	price.Amount *= purchase.Amount
	now := s.now().UTC().Unix()
	if err := changeShopInventory(ctx, tx, playerID, price, -1, now); err != nil {
		return 0, false, err
	}
	for _, reward := range purchase.Rewards {
		reward.Amount *= purchase.Amount
		if err := changeShopInventory(ctx, tx, playerID, reward, 1, now); err != nil {
			return 0, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO item_shop_transactions(player_id,transaction_id,shop_id,product_id,amount,created_at) VALUES(?,?,?,?,?,?)`, playerID, purchase.TransactionID, purchase.ShopID, purchase.ProductID, purchase.Amount, now); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
		return 0, false, err
	}
	return count + purchase.Amount, false, tx.Commit()
}

func shopPurchaseCountTx(ctx context.Context, tx *sql.Tx, playerID, shopID, productID string) (int64, error) {
	var count int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(amount),0) FROM item_shop_transactions WHERE player_id=? AND shop_id=? AND product_id=?`, playerID, shopID, productID).Scan(&count)
	return count, err
}

func changeShopInventory(ctx context.Context, tx *sql.Tx, playerID string, change ShopInventoryChange, direction, now int64) error {
	if change.Amount <= 0 || change.Kind == "" {
		return nil
	}
	amount := change.Amount * direction
	if direction < 0 {
		var result sql.Result
		var err error
		switch change.Kind {
		case "currency":
			result, err = tx.ExecContext(ctx, `UPDATE player_currencies SET quantity=quantity+? WHERE player_id=? AND currency_id=? AND quantity>=?`, amount, playerID, change.ID, change.Amount)
		case "item":
			result, err = tx.ExecContext(ctx, `UPDATE player_items SET quantity=quantity+? WHERE player_id=? AND item_kind=? AND item_id=? AND quantity>=?`, amount, playerID, change.SubID, change.ID, change.Amount)
		case "poke_gold":
			result, err = tx.ExecContext(ctx, `UPDATE player_pack_state SET poke_gold=poke_gold+? WHERE player_id=? AND poke_gold>=?`, amount, playerID, change.Amount)
		default:
			return fmt.Errorf("%w: unsupported shop price", ErrRuleViolation)
		}
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrInsufficientInventory
		}
		return nil
	}
	switch change.Kind {
	case "card":
		_, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+excluded.quantity,last_received_at=excluded.last_received_at`, playerID, change.ID, change.Amount, now, now)
		if err != nil {
			return err
		}
		return addCardInAccountLanguage(ctx, tx, playerID, change.ID, change.Amount)
	case "currency":
		_, err := tx.ExecContext(ctx, `INSERT INTO player_currencies(player_id,currency_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,currency_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, change.ID, change.Amount)
		return err
	case "item":
		_, err := tx.ExecContext(ctx, `INSERT INTO player_items(player_id,item_kind,item_id,quantity,obtained_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,item_kind,item_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, change.SubID, change.ID, change.Amount, now)
		return err
	case "profile_decoration":
		_, err := tx.ExecContext(ctx, `INSERT INTO player_profile_decorations(player_id,decoration_id,quantity,obtained_at) VALUES(?,?,?,?) ON CONFLICT(player_id,decoration_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, change.ID, change.Amount, now)
		return err
	case "rental_deck":
		_, err := tx.ExecContext(ctx, `INSERT INTO player_rental_decks(player_id,rental_deck_id,used_count,obtained_at) VALUES(?,?,0,?) ON CONFLICT(player_id,rental_deck_id) DO NOTHING`, playerID, change.ID, now)
		return err
	case "theme_deck_recipe":
		_, err := tx.ExecContext(ctx, `INSERT INTO player_theme_deck_recipes(player_id,theme_deck_recipe_id,obtained_at) VALUES(?,?,?) ON CONFLICT(player_id,theme_deck_recipe_id) DO NOTHING`, playerID, change.ID, now)
		return err
	default:
		return fmt.Errorf("%w: unsupported shop reward", ErrRuleViolation)
	}
}
