package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PlayerProfileSummary loads only the state required by the administration
// profile header. Large inventories remain behind their dedicated reads.
func (s *Store) PlayerProfileSummary(ctx context.Context, playerID string) (ProfileSummary, error) {
	var result ProfileSummary
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
SELECT p.player_id,p.display_name,p.level,p.experience,p.state_version,p.created_at,p.updated_at,
       s.language,s.country,s.year_of_birth,s.month_of_birth,s.icon_id,s.message_id,
       (SELECT COUNT(*) FROM player_cards WHERE player_id=p.player_id AND quantity>0)
FROM players p
JOIN player_settings s ON s.player_id=p.player_id
WHERE p.player_id=?`, playerID).Scan(
		&result.Player.ID, &result.Player.DisplayName, &result.Player.Level, &result.Player.Experience,
		&result.Player.StateVersion, &createdAt, &updatedAt,
		&result.Settings.Language, &result.Settings.Country, &result.Settings.YearOfBirth, &result.Settings.MonthOfBirth,
		&result.Settings.IconID, &result.Settings.MessageID, &result.CardCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileSummary{}, fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	}
	if err != nil {
		return ProfileSummary{}, fmt.Errorf("load player profile summary: %w", err)
	}
	result.Player.CreatedAt = time.Unix(createdAt, 0).UTC()
	result.Player.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	progress, err := s.tutorialProgress(ctx, playerID)
	if err != nil {
		return ProfileSummary{}, err
	}
	result.Tutorial = progress
	result.TutorialComplete = hasCompletedTutorial(progress, completedTutorial)
	return result, nil
}

func (s *Store) Snapshot(ctx context.Context, playerID string) (Snapshot, error) {
	player, err := s.Player(ctx, playerID)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Player: player}
	if err := s.db.QueryRowContext(ctx, `SELECT language,country,year_of_birth,month_of_birth,icon_id,message_id FROM player_settings WHERE player_id=?`, playerID).Scan(&result.Settings.Language, &result.Settings.Country, &result.Settings.YearOfBirth, &result.Settings.MonthOfBirth, &result.Settings.IconID, &result.Settings.MessageID); err != nil {
		return Snapshot{}, fmt.Errorf("load player settings: %w", err)
	}
	cardRows, err := s.db.QueryContext(ctx, `SELECT card_id,quantity,first_received_at,last_received_at FROM player_cards WHERE player_id=? ORDER BY card_id`, playerID)
	if err != nil {
		return Snapshot{}, err
	}
	for cardRows.Next() {
		var v CardStock
		var first, last int64
		if err := cardRows.Scan(&v.CardID, &v.Quantity, &first, &last); err != nil {
			cardRows.Close()
			return Snapshot{}, err
		}
		v.FirstReceivedAt = time.Unix(first, 0).UTC()
		v.LastReceivedAt = time.Unix(last, 0).UTC()
		result.Cards = append(result.Cards, v)
	}
	if err := cardRows.Close(); err != nil {
		return Snapshot{}, err
	}
	languageRows, err := s.db.QueryContext(ctx, `SELECT card_id,language,quantity FROM player_card_languages WHERE player_id=? ORDER BY card_id,language`, playerID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load player card languages: %w", err)
	}
	languageAmounts := map[string]map[int32]int64{}
	for languageRows.Next() {
		var cardID string
		var language int32
		var quantity int64
		if err := languageRows.Scan(&cardID, &language, &quantity); err != nil {
			languageRows.Close()
			return Snapshot{}, err
		}
		if languageAmounts[cardID] == nil {
			languageAmounts[cardID] = map[int32]int64{}
		}
		languageAmounts[cardID][language] = quantity
	}
	if err := languageRows.Close(); err != nil {
		return Snapshot{}, err
	}
	for index := range result.Cards {
		amounts := languageAmounts[result.Cards[index].CardID]
		if amounts == nil {
			amounts = map[int32]int64{}
		}
		normalizeCardLanguageAmounts(amounts, result.Cards[index].Quantity)
		for currentLanguage := int32(1); currentLanguage <= 12; currentLanguage++ {
			if amount := amounts[currentLanguage]; amount > 0 {
				result.Cards[index].Languages = append(result.Cards[index].Languages, CardLanguageStock{Language: currentLanguage, Quantity: amount})
			}
		}
	}
	currencyRows, err := s.db.QueryContext(ctx, `SELECT currency_id,quantity FROM player_currencies WHERE player_id=? ORDER BY currency_id`, playerID)
	if err != nil {
		return Snapshot{}, err
	}
	for currencyRows.Next() {
		var v CurrencyStock
		if err := currencyRows.Scan(&v.CurrencyID, &v.Quantity); err != nil {
			currencyRows.Close()
			return Snapshot{}, err
		}
		result.Currencies = append(result.Currencies, v)
	}
	if err := currencyRows.Close(); err != nil {
		return Snapshot{}, err
	}
	itemRows, err := s.db.QueryContext(ctx, `SELECT item_id,item_kind,quantity,obtained_at FROM player_items WHERE player_id=? ORDER BY item_kind,item_id`, playerID)
	if err != nil {
		return Snapshot{}, err
	}
	for itemRows.Next() {
		var v ItemStock
		var obtained int64
		if err := itemRows.Scan(&v.ItemID, &v.Kind, &v.Quantity, &obtained); err != nil {
			itemRows.Close()
			return Snapshot{}, err
		}
		v.ObtainedAt = time.Unix(obtained, 0).UTC()
		result.Items = append(result.Items, v)
	}
	if err := itemRows.Close(); err != nil {
		return Snapshot{}, err
	}
	decorationRows, err := s.db.QueryContext(ctx, `SELECT decoration_id,quantity,obtained_at FROM player_profile_decorations WHERE player_id=? ORDER BY decoration_id`, playerID)
	if err != nil {
		return Snapshot{}, err
	}
	for decorationRows.Next() {
		var value CosmeticStock
		var obtained int64
		if err := decorationRows.Scan(&value.CosmeticID, &value.Quantity, &obtained); err != nil {
			decorationRows.Close()
			return Snapshot{}, err
		}
		value.ObtainedAt = time.Unix(obtained, 0).UTC()
		result.ProfileDecorations = append(result.ProfileDecorations, value)
	}
	if err := decorationRows.Close(); err != nil {
		return Snapshot{}, err
	}
	for _, spec := range []struct {
		query  string
		target *[]CardCosmeticStock
	}{{`SELECT card_id,skin_id,quantity FROM player_card_skins WHERE player_id=? ORDER BY card_id,skin_id`, &result.CardSkins}, {`SELECT card_id,frame_id,quantity FROM player_card_frames WHERE player_id=? ORDER BY card_id,frame_id`, &result.CardFrames}} {
		rows, err := s.db.QueryContext(ctx, spec.query, playerID)
		if err != nil {
			return Snapshot{}, err
		}
		for rows.Next() {
			var value CardCosmeticStock
			if err := rows.Scan(&value.CardID, &value.CosmeticID, &value.Quantity); err != nil {
				rows.Close()
				return Snapshot{}, err
			}
			*spec.target = append(*spec.target, value)
		}
		if err := rows.Close(); err != nil {
			return Snapshot{}, err
		}
	}
	result.Tutorial, err = s.tutorialProgress(ctx, playerID)
	if err != nil {
		return Snapshot{}, err
	}
	result.TutorialComplete = hasCompletedTutorial(result.Tutorial, completedTutorial)
	return result, nil
}

func (s *Store) tutorialProgress(ctx context.Context, playerID string) ([]TutorialStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tutorial_id,step,completed FROM tutorial_progress WHERE player_id=? ORDER BY tutorial_id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("load tutorial progress: %w", err)
	}
	defer rows.Close()
	var result []TutorialStep
	for rows.Next() {
		var value TutorialStep
		if err := rows.Scan(&value.TutorialID, &value.Step, &value.Completed); err != nil {
			return nil, fmt.Errorf("scan tutorial progress: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tutorial progress: %w", err)
	}
	return result, nil
}

func (s *Store) StorageEntries(ctx context.Context, playerID string, keys []string) ([]StorageEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,value,created_at,updated_at FROM player_storage WHERE player_id=? ORDER BY key`, playerID)
	if err != nil {
		return nil, fmt.Errorf("load storage: %w", err)
	}
	defer rows.Close()
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	var result []StorageEntry
	for rows.Next() {
		var v StorageEntry
		var created, updated int64
		if err := rows.Scan(&v.Key, &v.Value, &created, &updated); err != nil {
			return nil, err
		}
		if len(wanted) > 0 && !wanted[v.Key] {
			continue
		}
		v.Value = append([]byte(nil), v.Value...)
		v.CreatedAt = time.Unix(created, 0).UTC()
		v.UpdatedAt = time.Unix(updated, 0).UTC()
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Store) SetStorageEntries(ctx context.Context, playerID string, entries []StorageEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	for _, entry := range entries {
		if entry.Key == "" {
			return fmt.Errorf("storage key is required")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_storage(player_id,key,value,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, playerID, entry.Key, entry.Value, now, now); err != nil {
			return fmt.Errorf("set storage entry %q: %w", entry.Key, err)
		}
	}
	return tx.Commit()
}

func (s *Store) ActivePlayer(ctx context.Context, deviceAccount string) (Player, error) {
	p, err := scanPlayer(s.db.QueryRowContext(ctx, `SELECT p.player_id,p.display_name,p.level,p.experience,p.state_version,p.created_at,p.updated_at FROM active_profiles a JOIN players p ON p.player_id=a.player_id WHERE a.device_account=?`, deviceAccount))
	if errors.Is(err, sql.ErrNoRows) {
		return Player{}, fmt.Errorf("%w: active profile for device %q", ErrNotFound, deviceAccount)
	}
	if err != nil {
		return Player{}, err
	}
	p.DeviceAccount = deviceAccount
	return p, nil
}
