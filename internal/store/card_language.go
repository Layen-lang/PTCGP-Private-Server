package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/gamelocale"
)

func addCardInAccountLanguage(ctx context.Context, tx *sql.Tx, playerID, cardID string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	var locale string
	if err := tx.QueryRowContext(ctx, `SELECT language FROM player_settings WHERE player_id=?`, playerID).Scan(&locale); err != nil {
		return fmt.Errorf("load player card language: %w", err)
	}
	language, ok := gamelocale.LanguageForLocale(locale)
	if !ok {
		return fmt.Errorf("unsupported stored player language %q", locale)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO player_card_languages(player_id,card_id,language,quantity) VALUES(?,?,?,?) ON CONFLICT(player_id,card_id,language) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, cardID, language, amount)
	return err
}
