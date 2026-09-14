package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/gamelocale"
)

type InitialItem struct {
	ID, Kind string
	Quantity int64
}

type InitialCardLanguage struct {
	CardID   string
	Language int32
	Quantity int64
}

type InitialInventory struct {
	Cards, Currencies, ProfileDecorations map[string]int64
	Items                                 []InitialItem
	CardLanguages                         []InitialCardLanguage
	CardSkins, CardFrames                 []CardCosmeticStock
	RentalDecks                           []string
	PokeGold                              int64
}

func (s *Store) CreatePlayerWithInventory(ctx context.Context, displayName string, level int, experience int64, tutorialComplete bool, inventory InitialInventory) (Player, error) {
	return s.CreatePlayerWithInventoryAndSettings(ctx, displayName, level, experience, tutorialComplete, inventory, gamelocale.DefaultLocale, gamelocale.DefaultCountry)
}

// CreatePlayerWithInventoryAndSettings creates a populated player with explicit account preferences.
func (s *Store) CreatePlayerWithInventoryAndSettings(ctx context.Context, displayName string, level int, experience int64, tutorialComplete bool, inventory InitialInventory, language, country string) (Player, error) {
	cardLanguage, ok := gamelocale.LanguageForLocale(language)
	if !ok {
		return Player{}, fmt.Errorf("unsupported initial card language %q", language)
	}
	now := s.now().UTC()
	id, err := randomUUID()
	if err != nil {
		return Player{}, err
	}
	created := Player{ID: id, DisplayName: displayName, Level: level, Experience: experience, StateVersion: 1, CreatedAt: now, UpdatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Player{}, fmt.Errorf("begin create populated player: %w", err)
	}
	defer tx.Rollback()
	if err := insertPlayer(ctx, tx, created, tutorialComplete, language, country); err != nil {
		return Player{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET display_order=(SELECT COALESCE(MAX(display_order),0)+1 FROM players WHERE player_id<>?) WHERE player_id=?`, created.ID, created.ID); err != nil {
		return Player{}, fmt.Errorf("order populated player: %w", err)
	}
	if err := seedInventory(ctx, tx, created.ID, now, inventory, cardLanguage); err != nil {
		return Player{}, err
	}
	if err := tx.Commit(); err != nil {
		return Player{}, fmt.Errorf("commit populated player: %w", err)
	}
	return created, nil
}

func seedInventory(ctx context.Context, tx *sql.Tx, playerID string, now time.Time, inventory InitialInventory, defaultCardLanguage int32) error {
	for cardID, quantity := range inventory.Cards {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?)`, playerID, cardID, quantity, now.Unix(), now.Unix()); err != nil {
			return fmt.Errorf("seed card %q: %w", cardID, err)
		}
	}
	if len(inventory.CardLanguages) == 0 {
		for cardID, quantity := range inventory.Cards {
			if quantity > 0 {
				if _, err := tx.ExecContext(ctx, `INSERT INTO player_card_languages(player_id,card_id,language,quantity) VALUES(?,?,?,?)`, playerID, cardID, defaultCardLanguage, quantity); err != nil {
					return fmt.Errorf("seed card language quantity %q: %w", cardID, err)
				}
			}
		}
	} else {
		for _, value := range inventory.CardLanguages {
			if _, err := tx.ExecContext(ctx, `INSERT INTO player_card_languages(player_id,card_id,language,quantity) VALUES(?,?,?,?)`, playerID, value.CardID, value.Language, value.Quantity); err != nil {
				return fmt.Errorf("seed card language %q/%d: %w", value.CardID, value.Language, err)
			}
		}
	}
	for currencyID, quantity := range inventory.Currencies {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_currencies(player_id,currency_id,quantity) VALUES(?,?,?)`, playerID, currencyID, quantity); err != nil {
			return fmt.Errorf("seed currency %q: %w", currencyID, err)
		}
	}
	for _, item := range inventory.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_items(player_id,item_kind,item_id,quantity,obtained_at) VALUES(?,?,?,?,?)`, playerID, item.Kind, item.ID, item.Quantity, now.Unix()); err != nil {
			return fmt.Errorf("seed item %q: %w", item.ID, err)
		}
	}
	for cosmeticID, quantity := range inventory.ProfileDecorations {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_profile_decorations(player_id,decoration_id,quantity,obtained_at) VALUES(?,?,?,?)`, playerID, cosmeticID, quantity, now.Unix()); err != nil {
			return fmt.Errorf("seed profile decoration %q: %w", cosmeticID, err)
		}
	}
	for _, value := range inventory.CardSkins {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_card_skins(player_id,card_id,skin_id,quantity) VALUES(?,?,?,?)`, playerID, value.CardID, value.CosmeticID, value.Quantity); err != nil {
			return fmt.Errorf("seed card skin %q/%q: %w", value.CardID, value.CosmeticID, err)
		}
	}
	for _, value := range inventory.CardFrames {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_card_frames(player_id,card_id,frame_id,quantity) VALUES(?,?,?,?)`, playerID, value.CardID, value.CosmeticID, value.Quantity); err != nil {
			return fmt.Errorf("seed card frame %q/%q: %w", value.CardID, value.CosmeticID, err)
		}
	}
	for _, rentalDeckID := range inventory.RentalDecks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_rental_decks(player_id,rental_deck_id,used_count,obtained_at) VALUES(?,?,0,?)`, playerID, rentalDeckID, now.Unix()); err != nil {
			return fmt.Errorf("seed rental deck %q: %w", rentalDeckID, err)
		}
	}
	if inventory.PokeGold > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE player_pack_state SET poke_gold=? WHERE player_id=?`, inventory.PokeGold, playerID); err != nil {
			return fmt.Errorf("seed Poké Gold: %w", err)
		}
	}
	return nil
}

func (s *Store) DuplicatePlayer(ctx context.Context, sourceID, displayName string) (Player, error) {
	source, err := s.Player(ctx, sourceID)
	if err != nil {
		return Player{}, err
	}
	now := s.now().UTC()
	id, err := randomUUID()
	if err != nil {
		return Player{}, err
	}
	duplicate := Player{ID: id, DisplayName: displayName, Level: source.Level, Experience: source.Experience, StateVersion: 1, CreatedAt: now, UpdatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Player{}, fmt.Errorf("begin duplicate player: %w", err)
	}
	defer tx.Rollback()
	if err := insertPlayer(ctx, tx, duplicate, false, gamelocale.DefaultLocale, gamelocale.DefaultCountry); err != nil {
		return Player{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET display_order=(SELECT COALESCE(MAX(display_order),0)+1 FROM players WHERE player_id<>?) WHERE player_id=?`, duplicate.ID, duplicate.ID); err != nil {
		return Player{}, err
	}
	statements := []string{
		`DELETE FROM player_settings WHERE player_id=?`,
		`INSERT INTO player_settings SELECT ?,language,country,year_of_birth,month_of_birth,icon_id,message_id FROM player_settings WHERE player_id=?`,
		`DELETE FROM tutorial_progress WHERE player_id=?`,
		`INSERT INTO tutorial_progress SELECT ?,tutorial_id,step,completed,? FROM tutorial_progress WHERE player_id=?`,
		`INSERT INTO player_cards SELECT ?,card_id,quantity,?,? FROM player_cards WHERE player_id=?`,
		`INSERT INTO player_card_languages SELECT ?,card_id,language,quantity FROM player_card_languages WHERE player_id=?`,
		`INSERT INTO player_currencies SELECT ?,currency_id,quantity FROM player_currencies WHERE player_id=?`,
		`INSERT INTO player_items SELECT ?,item_kind,item_id,quantity,? FROM player_items WHERE player_id=?`,
		`INSERT INTO player_profile_decorations SELECT ?,decoration_id,quantity,? FROM player_profile_decorations WHERE player_id=?`,
		`INSERT INTO player_selected_emblems SELECT ?,display_order,emblem_id FROM player_selected_emblems WHERE player_id=?`,
		`INSERT INTO player_card_skins SELECT ?,card_id,skin_id,quantity FROM player_card_skins WHERE player_id=?`,
		`INSERT INTO player_card_frames SELECT ?,card_id,frame_id,quantity FROM player_card_frames WHERE player_id=?`,
		`DELETE FROM player_pack_state WHERE player_id=?`,
		`INSERT INTO player_pack_state SELECT ?,pack_power,?,poke_gold FROM player_pack_state WHERE player_id=?`,
		`INSERT INTO player_pack_ceil_points SELECT ?,group_id,quantity FROM player_pack_ceil_points WHERE player_id=?`,
		`INSERT INTO player_rental_decks SELECT ?,rental_deck_id,used_count,? FROM player_rental_decks WHERE player_id=?`,
		`INSERT INTO player_theme_deck_recipes SELECT ?,theme_deck_recipe_id,? FROM player_theme_deck_recipes WHERE player_id=?`,
	}
	arguments := [][]any{
		{duplicate.ID}, {duplicate.ID, sourceID}, {duplicate.ID}, {duplicate.ID, now.Unix(), sourceID},
		{duplicate.ID, now.Unix(), now.Unix(), sourceID}, {duplicate.ID, sourceID}, {duplicate.ID, sourceID}, {duplicate.ID, now.Unix(), sourceID},
		{duplicate.ID, now.Unix(), sourceID}, {duplicate.ID, sourceID}, {duplicate.ID, sourceID}, {duplicate.ID, sourceID},
		{duplicate.ID}, {duplicate.ID, now.Unix(), sourceID}, {duplicate.ID, sourceID},
		{duplicate.ID, now.Unix(), sourceID}, {duplicate.ID, now.Unix(), sourceID},
	}
	for index, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, arguments[index]...); err != nil {
			return Player{}, fmt.Errorf("duplicate player state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Player{}, fmt.Errorf("commit duplicate player: %w", err)
	}
	return duplicate, nil
}

func (s *Store) ReorderPlayers(ctx context.Context, playerIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorder players: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM players`).Scan(&count); err != nil {
		return err
	}
	if len(playerIDs) != count {
		return fmt.Errorf("player order must contain every player exactly once")
	}
	seen := make(map[string]bool, len(playerIDs))
	for index, playerID := range playerIDs {
		if playerID == "" || seen[playerID] {
			return fmt.Errorf("player order contains an invalid duplicate")
		}
		seen[playerID] = true
		result, err := tx.ExecContext(ctx, `UPDATE players SET display_order=? WHERE player_id=?`, index, playerID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
		}
	}
	return tx.Commit()
}

func (s *Store) DeletePlayer(ctx context.Context, playerID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete player: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM players WHERE player_id=?`, playerID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	} else if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT device_account FROM active_profiles WHERE player_id=?`, playerID)
	if err != nil {
		return err
	}
	var devices []string
	for rows.Next() {
		var device string
		if err := rows.Scan(&device); err != nil {
			rows.Close()
			return err
		}
		devices = append(devices, device)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET authorized_player_id=NULL WHERE authorized_player_id=?`, playerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM players WHERE player_id=?`, playerID); err != nil {
		return fmt.Errorf("delete player: %w", err)
	}
	for _, device := range devices {
		var replacement string
		err := tx.QueryRowContext(ctx, `SELECT p.player_id FROM players p LEFT JOIN device_players d ON d.player_id=p.player_id AND d.device_account=? ORDER BY CASE WHEN d.player_id IS NULL THEN 1 ELSE 0 END,p.display_order,p.created_at LIMIT 1`, device).Scan(&replacement)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO device_players(device_account,player_id,linked_at) VALUES(?,?,?)`, device, replacement, s.now().UTC().Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO active_profiles(device_account,player_id,selected_at) VALUES(?,?,?) ON CONFLICT(device_account) DO UPDATE SET player_id=excluded.player_id,selected_at=excluded.selected_at`, device, replacement, s.now().UTC().Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_account=? AND revoked_at IS NULL`, s.now().UTC().Unix(), device); err != nil {
			return err
		}
	}
	return tx.Commit()
}
