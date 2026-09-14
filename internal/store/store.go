// Package store owns durable device, player, inventory, session, and
// idempotency state. It contains no master-data or protobuf structures.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/gamelocale"
	_ "modernc.org/sqlite"
)

var ErrInvalidSession = errors.New("invalid session")
var ErrInvalidDeviceCredentials = errors.New("invalid device credentials")
var ErrNotFound = errors.New("not found")
var ErrExpired = errors.New("expired")
var ErrInsufficientInventory = errors.New("insufficient inventory")

type Player struct {
	ID            string
	DeviceAccount string // populated only when resolved through a device/session
	DisplayName   string
	Level         int
	Experience    int64
	StateVersion  int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Device struct {
	Account            string
	LastSeenAt         time.Time
	LastAuthorizedAt   time.Time
	ActivePlayerID     string
	AuthorizedPlayerID string
}

type Settings struct {
	Language, Country, IconID, MessageID string
	YearOfBirth, MonthOfBirth            int
}

type CardStock struct {
	CardID                          string
	Quantity                        int64
	Languages                       []CardLanguageStock
	FirstReceivedAt, LastReceivedAt time.Time
}

type CardLanguageStock struct {
	Language int32
	Quantity int64
}

type CurrencyStock struct {
	CurrencyID string
	Quantity   int64
}

type ItemStock struct {
	ItemID, Kind string
	Quantity     int64
	ObtainedAt   time.Time
}

type CosmeticStock struct {
	CosmeticID string
	Quantity   int64
	ObtainedAt time.Time
}

type CardCosmeticStock struct {
	CardID, CosmeticID string
	Quantity           int64
}

type TutorialStep struct {
	TutorialID string
	Step       int64
	Completed  bool
}

type StorageEntry struct {
	Key       string
	Value     []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Snapshot struct {
	Player             Player
	Settings           Settings
	Cards              []CardStock
	Currencies         []CurrencyStock
	Items              []ItemStock
	ProfileDecorations []CosmeticStock
	CardSkins          []CardCosmeticStock
	CardFrames         []CardCosmeticStock
	Tutorial           []TutorialStep
	TutorialComplete   bool
}

// ProfileSummary contains the lightweight state needed to identify and edit a
// player without loading their inventories or card cosmetics.
type ProfileSummary struct {
	Player           Player
	Settings         Settings
	Tutorial         []TutorialStep
	TutorialComplete bool
	CardCount        int
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) RegisterOrValidateDevice(ctx context.Context, deviceAccount, password string) error {
	if deviceAccount == "" || password == "" {
		return ErrInvalidDeviceCredentials
	}
	hash := sha256.Sum256([]byte(password))
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin device transaction: %w", err)
	}
	defer tx.Rollback()
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT password_hash FROM devices WHERE device_account=?`, deviceAccount).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO devices(device_account,password_hash,created_at,last_seen_at) VALUES(?,?,?,?)`, deviceAccount, hash[:], now, now)
	case err != nil:
		return fmt.Errorf("load device credentials: %w", err)
	case len(existing) == 0:
		_, err = tx.ExecContext(ctx, `UPDATE devices SET password_hash=?,last_seen_at=? WHERE device_account=?`, hash[:], now, deviceAccount)
	case len(existing) != len(hash) || subtle.ConstantTimeCompare(existing, hash[:]) != 1:
		return ErrInvalidDeviceCredentials
	default:
		_, err = tx.ExecContext(ctx, `UPDATE devices SET last_seen_at=? WHERE device_account=?`, now, deviceAccount)
	}
	if err != nil {
		return fmt.Errorf("store device credentials: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit device credentials: %w", err)
	}
	return nil
}

// EnsurePlayer is retained for the BaaS adapter. It returns the device's active
// profile and reuses the most recently used local profile for a new device.
// A default profile is created only when the local database has no profile.
func (s *Store) EnsurePlayer(ctx context.Context, deviceAccount string) (Player, bool, error) {
	if deviceAccount == "" {
		return Player{}, false, fmt.Errorf("device account is required")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Player{}, false, fmt.Errorf("begin player transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO devices(device_account,created_at,last_seen_at) VALUES(?,?,?) ON CONFLICT(device_account) DO UPDATE SET last_seen_at=excluded.last_seen_at`, deviceAccount, now.Unix(), now.Unix()); err != nil {
		return Player{}, false, fmt.Errorf("upsert device: %w", err)
	}
	player, err := scanPlayer(tx.QueryRowContext(ctx, `SELECT p.player_id,p.display_name,p.level,p.experience,p.state_version,p.created_at,p.updated_at FROM active_profiles a JOIN players p ON p.player_id=a.player_id WHERE a.device_account=?`, deviceAccount))
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		player, err = scanPlayer(tx.QueryRowContext(ctx, `SELECT p.player_id,p.display_name,p.level,p.experience,p.state_version,p.created_at,p.updated_at FROM pending_profile a JOIN players p ON p.player_id=a.player_id WHERE a.singleton=1`))
		if errors.Is(err, sql.ErrNoRows) {
			player, err = mostRecentlyUsedPlayer(ctx, tx)
			if errors.Is(err, sql.ErrNoRows) {
				player = initialPlayer(deviceAccount, now)
				created = true
				if err := insertPlayer(ctx, tx, player, true, gamelocale.DefaultLocale, gamelocale.DefaultCountry); err != nil {
					return Player{}, false, err
				}
			} else if err != nil {
				return Player{}, false, err
			}
		} else if err != nil {
			return Player{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM pending_profile WHERE singleton=1`); err != nil {
			return Player{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_players(device_account,player_id,linked_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, deviceAccount, player.ID, now.Unix()); err != nil {
			return Player{}, false, fmt.Errorf("link default player: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO active_profiles(device_account,player_id,selected_at) VALUES(?,?,?) ON CONFLICT(device_account) DO UPDATE SET player_id=excluded.player_id,selected_at=excluded.selected_at`, deviceAccount, player.ID, now.Unix()); err != nil {
			return Player{}, false, fmt.Errorf("activate default player: %w", err)
		}
	} else if err != nil {
		return Player{}, false, fmt.Errorf("load active player: %w", err)
	}
	player.DeviceAccount = deviceAccount
	if err := tx.Commit(); err != nil {
		return Player{}, false, fmt.Errorf("commit active player: %w", err)
	}
	return player, created, nil
}

func mostRecentlyUsedPlayer(ctx context.Context, tx *sql.Tx) (Player, error) {
	const columns = `p.player_id,p.display_name,p.level,p.experience,p.state_version,p.created_at,p.updated_at`
	queries := []string{
		`SELECT ` + columns + ` FROM devices d JOIN players p ON p.player_id=d.authorized_player_id WHERE d.last_authorized_at IS NOT NULL ORDER BY d.last_authorized_at DESC,d.last_seen_at DESC LIMIT 1`,
		`SELECT ` + columns + ` FROM active_profiles a JOIN players p ON p.player_id=a.player_id ORDER BY a.selected_at DESC LIMIT 1`,
		`SELECT ` + columns + ` FROM players p ORDER BY p.display_order,p.created_at,p.player_id LIMIT 1`,
	}
	for _, query := range queries {
		player, err := scanPlayer(tx.QueryRowContext(ctx, query))
		if err == nil {
			return player, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Player{}, fmt.Errorf("load most recently used player: %w", err)
		}
	}
	return Player{}, sql.ErrNoRows
}

func (s *Store) RecordAuthorization(ctx context.Context, deviceAccount, playerID string) error {
	const (
		comebackThreshold = 14 * 24 * time.Hour
		comebackCooldown  = 45 * 24 * time.Hour
	)
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousAuthorization, previousComeback sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT last_authorized_at,last_comeback_at FROM devices WHERE device_account=?`, deviceAccount).Scan(&previousAuthorization, &previousComeback); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("record authorization: %w: device %q", ErrNotFound, deviceAccount)
	} else if err != nil {
		return err
	}
	absenceDays := int64(0)
	eligible := false
	if previousAuthorization.Valid {
		absence := now.Sub(time.Unix(previousAuthorization.Int64, 0).UTC())
		cooldownElapsed := !previousComeback.Valid || now.Sub(time.Unix(previousComeback.Int64, 0).UTC()) >= comebackCooldown
		if absence >= comebackThreshold && cooldownElapsed {
			absenceDays = int64(absence / (24 * time.Hour))
			eligible = true
		}
	}
	var returnedAt, lastComeback any
	if eligible {
		returnedAt, lastComeback = now.Unix(), now.Unix()
	} else if previousComeback.Valid {
		lastComeback = previousComeback.Int64
	}
	result, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at=?,last_authorized_at=?,authorized_player_id=?,comeback_absence_days=?,comeback_returned_at=?,last_comeback_at=? WHERE device_account=?`, now.Unix(), now.Unix(), playerID, absenceDays, returnedAt, lastComeback, deviceAccount)
	if err != nil {
		return fmt.Errorf("record authorization: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("record authorization: %w: device %q", ErrNotFound, deviceAccount)
	}
	return tx.Commit()
}

type ComebackInfo struct {
	IsReturning bool
	AbsenceDays int32
	ReturnedAt  time.Time
}

func (s *Store) ComebackInfo(ctx context.Context, deviceAccount string) (ComebackInfo, error) {
	var result ComebackInfo
	var returnedAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT comeback_absence_days,comeback_returned_at FROM devices WHERE device_account=?`, deviceAccount).Scan(&result.AbsenceDays, &returnedAt); errors.Is(err, sql.ErrNoRows) {
		return ComebackInfo{}, fmt.Errorf("%w: device %q", ErrNotFound, deviceAccount)
	} else if err != nil {
		return ComebackInfo{}, err
	}
	result.IsReturning = result.AbsenceDays >= 14 && returnedAt.Valid
	if returnedAt.Valid {
		result.ReturnedAt = time.Unix(returnedAt.Int64, 0).UTC()
	}
	return result, nil
}

func (s *Store) DeviceCreatedAt(ctx context.Context, deviceAccount string) (time.Time, error) {
	var created int64
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM devices WHERE device_account=?`, deviceAccount).Scan(&created); errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("%w: device %q", ErrNotFound, deviceAccount)
	} else if err != nil {
		return time.Time{}, fmt.Errorf("load device creation: %w", err)
	}
	return time.Unix(created, 0).UTC(), nil
}

func (s *Store) CreatePlayer(ctx context.Context, displayName string, level int, experience int64, tutorialComplete bool) (Player, error) {
	return s.CreatePlayerWithSettings(ctx, displayName, level, experience, tutorialComplete, gamelocale.DefaultLocale, gamelocale.DefaultCountry)
}

// CreatePlayerWithSettings creates a player with explicit account preferences.
func (s *Store) CreatePlayerWithSettings(ctx context.Context, displayName string, level int, experience int64, tutorialComplete bool, language, country string) (Player, error) {
	now := s.now().UTC()
	id, err := randomUUID()
	if err != nil {
		return Player{}, err
	}
	player := Player{ID: id, DisplayName: displayName, Level: level, Experience: experience, StateVersion: 1, CreatedAt: now, UpdatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Player{}, fmt.Errorf("begin create player: %w", err)
	}
	defer tx.Rollback()
	if err := insertPlayer(ctx, tx, player, tutorialComplete, language, country); err != nil {
		return Player{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET display_order=(SELECT COALESCE(MAX(display_order),0)+1 FROM players WHERE player_id<>?) WHERE player_id=?`, player.ID, player.ID); err != nil {
		return Player{}, fmt.Errorf("order created player: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Player{}, fmt.Errorf("commit create player: %w", err)
	}
	return player, nil
}

func insertPlayer(ctx context.Context, tx *sql.Tx, player Player, tutorialComplete bool, language, country string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO players(player_id,display_name,level,experience,state_version,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, player.ID, player.DisplayName, player.Level, player.Experience, player.StateVersion, player.CreatedAt.Unix(), player.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert player: %w", err)
	}
	for level := 1; level <= player.Level; level++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_level_histories(player_id,player_level,leveled_up_at) VALUES(?,?,?)`, player.ID, level, player.CreatedAt.Unix()); err != nil {
			return fmt.Errorf("seed player level history: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO player_settings(player_id,language,country,year_of_birth,month_of_birth,icon_id,message_id) VALUES(?,?,?,?,?,?,?)`, player.ID, language, country, 2000, 1, "PROFILE_ICON_100140_KABIGON", "PROFILE_MESSAGE_1")
	if err != nil {
		return fmt.Errorf("insert player settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_pack_state(player_id,pack_power,pack_power_updated_at,poke_gold) VALUES(?,?,?,?)`, player.ID, 2, player.UpdatedAt.Unix(), 0); err != nil {
		return fmt.Errorf("insert player pack state: %w", err)
	}
	return setTutorialTx(ctx, tx, player.ID, tutorialComplete, completedTutorial, player.UpdatedAt.Unix())
}

func (s *Store) Players(ctx context.Context) ([]Player, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT player_id,display_name,level,experience,state_version,created_at,updated_at FROM players ORDER BY display_order,created_at,player_id`)
	if err != nil {
		return nil, fmt.Errorf("list players: %w", err)
	}
	defer rows.Close()
	var result []Player
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan player: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) Player(ctx context.Context, playerID string) (Player, error) {
	p, err := scanPlayer(s.db.QueryRowContext(ctx, `SELECT player_id,display_name,level,experience,state_version,created_at,updated_at FROM players WHERE player_id=?`, playerID))
	if errors.Is(err, sql.ErrNoRows) {
		return Player{}, fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	}
	if err != nil {
		return Player{}, fmt.Errorf("load player: %w", err)
	}
	return p, nil
}

func (s *Store) UpdatePlayer(ctx context.Context, playerID, displayName string, level int, experience int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousLevel int
	if err := tx.QueryRowContext(ctx, `SELECT level FROM players WHERE player_id=?`, playerID).Scan(&previousLevel); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	} else if err != nil {
		return err
	}
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE players SET display_name=?,level=?,experience=?,state_version=state_version+1,updated_at=? WHERE player_id=?`, displayName, level, experience, now, playerID)
	if err != nil {
		return fmt.Errorf("update player: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	}
	for reached := previousLevel + 1; reached <= level; reached++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_level_histories(player_id,player_level,leveled_up_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, playerID, reached, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) NicknameChangedAt(ctx context.Context, playerID string) (time.Time, bool, error) {
	var changedAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT nickname_changed_at FROM players WHERE player_id=?`, playerID).Scan(&changedAt); errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	} else if err != nil {
		return time.Time{}, false, err
	}
	if !changedAt.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(changedAt.Int64, 0).UTC(), true, nil
}

func (s *Store) UpdateNickname(ctx context.Context, playerID, displayName string, cooldown time.Duration) error {
	now := s.now().UTC()
	cutoff := now.Add(-cooldown).Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE players SET display_name=?,nickname_changed_at=?,state_version=state_version+1,updated_at=? WHERE player_id=? AND (nickname_changed_at IS NULL OR nickname_changed_at<=?)`, displayName, now.Unix(), now.Unix(), playerID, cutoff)
	if err != nil {
		return fmt.Errorf("update nickname: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed == 1 {
		return nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM players WHERE player_id=?`, playerID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	}
	return fmt.Errorf("%w: nickname can only be changed once every 30 days", ErrRuleViolation)
}

func (s *Store) UpdateProfileSettings(ctx context.Context, playerID, iconID, messageID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE player_settings SET icon_id=?,message_id=? WHERE player_id=?`, iconID, messageID, playerID)
	if err != nil {
		return fmt.Errorf("update profile settings: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, s.now().UTC().Unix(), playerID); err != nil {
		return fmt.Errorf("version profile settings: %w", err)
	}
	return tx.Commit()
}

func (s *Store) SetCurrency(ctx context.Context, playerID, currencyID string, quantity int64) error {
	return s.setQuantity(ctx, `INSERT INTO player_currencies(player_id,currency_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,currency_id) DO UPDATE SET quantity=excluded.quantity WHERE quantity<>excluded.quantity`, playerID, currencyID, "", quantity)
}
func (s *Store) SetCard(ctx context.Context, playerID, cardID string, quantity int64) error {
	_, err := s.SetCardLanguage(ctx, playerID, cardID, 4, quantity)
	return err
}

// SetCardLanguage replaces the quantity for one printed language and keeps the
// aggregate amount consumed by older gameplay code in sync.
func (s *Store) SetCardLanguage(ctx context.Context, playerID, cardID string, language int32, quantity int64) (int64, error) {
	if language < 1 || language > 12 {
		return 0, fmt.Errorf("card language must be between 1 and 12")
	}
	if quantity < 0 {
		return 0, fmt.Errorf("quantity must be non-negative")
	}
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var currentTotal, firstReceived int64
	err = tx.QueryRowContext(ctx, `SELECT quantity,first_received_at FROM player_cards WHERE player_id=? AND card_id=?`, playerID, cardID).Scan(&currentTotal, &firstReceived)
	if errors.Is(err, sql.ErrNoRows) {
		currentTotal, firstReceived = 0, now
	} else if err != nil {
		return 0, fmt.Errorf("load card quantity: %w", err)
	}
	amounts, err := cardLanguageAmounts(ctx, tx, playerID, cardID)
	if err != nil {
		return 0, err
	}
	normalizeCardLanguageAmounts(amounts, currentTotal)
	if quantity == 0 {
		delete(amounts, language)
	} else {
		amounts[language] = quantity
	}
	var total int64
	for _, amount := range amounts {
		total += amount
	}
	if total == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM player_cards WHERE player_id=? AND card_id=?`, playerID, cardID); err != nil {
			return 0, fmt.Errorf("delete card quantity: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=excluded.quantity,last_received_at=excluded.last_received_at`, playerID, cardID, total, firstReceived, now); err != nil {
			return 0, fmt.Errorf("set card total quantity: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM player_card_languages WHERE player_id=? AND card_id=?`, playerID, cardID); err != nil {
			return 0, fmt.Errorf("reset card language quantities: %w", err)
		}
		for currentLanguage := int32(1); currentLanguage <= 12; currentLanguage++ {
			if amount := amounts[currentLanguage]; amount > 0 {
				if _, err := tx.ExecContext(ctx, `INSERT INTO player_card_languages(player_id,card_id,language,quantity) VALUES(?,?,?,?)`, playerID, cardID, currentLanguage, amount); err != nil {
					return 0, fmt.Errorf("set card language quantity: %w", err)
				}
			}
		}
	}
	if total != currentTotal {
		if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
			return 0, fmt.Errorf("version player inventory: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

type cardLanguageQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func cardLanguageAmounts(ctx context.Context, query cardLanguageQuery, playerID, cardID string) (map[int32]int64, error) {
	rows, err := query.QueryContext(ctx, `SELECT language,quantity FROM player_card_languages WHERE player_id=? AND card_id=? ORDER BY language`, playerID, cardID)
	if err != nil {
		return nil, fmt.Errorf("load card language quantities: %w", err)
	}
	defer rows.Close()
	result := map[int32]int64{}
	for rows.Next() {
		var language int32
		var quantity int64
		if err := rows.Scan(&language, &quantity); err != nil {
			return nil, err
		}
		if quantity > 0 {
			result[language] = quantity
		}
	}
	return result, rows.Err()
}

func normalizeCardLanguageAmounts(amounts map[int32]int64, total int64) {
	var tagged int64
	for _, amount := range amounts {
		tagged += amount
	}
	if tagged < total {
		amounts[4] += total - tagged
		return
	}
	for remaining := tagged - total; remaining > 0; {
		language := int32(4)
		if amounts[language] == 0 {
			for candidate := int32(1); candidate <= 12; candidate++ {
				if amounts[candidate] > 0 {
					language = candidate
					break
				}
			}
		}
		removed := min(amounts[language], remaining)
		amounts[language] -= removed
		remaining -= removed
		if amounts[language] == 0 {
			delete(amounts, language)
		}
	}
}
func (s *Store) SetItem(ctx context.Context, playerID, kind, itemID string, quantity int64) error {
	return s.setQuantity(ctx, `INSERT INTO player_items(player_id,item_kind,item_id,quantity,obtained_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,item_kind,item_id) DO UPDATE SET quantity=excluded.quantity WHERE quantity<>excluded.quantity`, playerID, itemID, kind, quantity)
}

func (s *Store) setQuantity(ctx context.Context, query, playerID, id, kind string, quantity int64) error {
	if quantity < 0 {
		return fmt.Errorf("quantity must be non-negative")
	}
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if quantity == 0 {
		switch {
		case kind != "":
			result, err = tx.ExecContext(ctx, `DELETE FROM player_items WHERE player_id=? AND item_kind=? AND item_id=?`, playerID, kind, id)
		case stringsContains(query, "player_cards"):
			result, err = tx.ExecContext(ctx, `DELETE FROM player_cards WHERE player_id=? AND card_id=?`, playerID, id)
		default:
			result, err = tx.ExecContext(ctx, `DELETE FROM player_currencies WHERE player_id=? AND currency_id=?`, playerID, id)
		}
	} else if kind != "" {
		result, err = tx.ExecContext(ctx, query, playerID, kind, id, quantity, now)
	} else if stringsContains(query, "player_cards") {
		result, err = tx.ExecContext(ctx, query, playerID, id, quantity, now, now)
	} else {
		result, err = tx.ExecContext(ctx, query, playerID, id, quantity)
	}
	if err != nil {
		return fmt.Errorf("set inventory quantity: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect inventory update: %w", err)
	}
	if changed == 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
		return fmt.Errorf("version player inventory: %w", err)
	}
	return tx.Commit()
}

func stringsContains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func (s *Store) SetTutorialComplete(ctx context.Context, playerID string, complete bool) error {
	return s.setTutorialProgress(ctx, playerID, complete, completedTutorial)
}

// SetTutorialCompletions replaces a player's progress with the complete list
// derived from the active master data and client version.
func (s *Store) SetTutorialCompletions(ctx context.Context, playerID string, completions []TutorialStep) error {
	return s.setTutorialProgress(ctx, playerID, true, completions)
}

func (s *Store) setTutorialProgress(ctx context.Context, playerID string, complete bool, completions []TutorialStep) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	if err := setTutorialTx(ctx, tx, playerID, complete, completions, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordTutorialCompletion durably records the highest completed step reported
// by the client. Replayed or out-of-order requests never move progress back.
func (s *Store) RecordTutorialCompletion(ctx context.Context, playerID, tutorialID string, step int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tutorial completion: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `
INSERT INTO tutorial_progress(player_id,tutorial_id,step,completed,updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(player_id,tutorial_id) DO UPDATE SET
  step=excluded.step,
  completed=1,
  updated_at=excluded.updated_at
WHERE tutorial_progress.step < excluded.step OR tutorial_progress.completed=0`, playerID, tutorialID, step, true, now)
	if err != nil {
		return fmt.Errorf("record tutorial completion: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect tutorial completion: %w", err)
	}
	if changed == 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
		return fmt.Errorf("version tutorial completion: %w", err)
	}
	return tx.Commit()
}

// completedTutorial is the versioned migration snapshot for master-data 1.7.2
// and client 1.7.2. Runtime profile changes use the active catalog instead.
var completedTutorial = []TutorialStep{
	{"1000", 999, true}, {"1010", 2, true}, {"1011", 3, true},
	{"2000", 3, true}, {"2001", 5, true}, {"2002", 5, true}, {"2003", 5, true},
	{"2010", 2, true}, {"2140", 6, true}, {"2160", 5, true}, {"2161", 2, true},
	{"2162", 2, true}, {"2300", 2, true}, {"2400", 2, true},
	{"2500", 2, true}, {"2510", 2, true}, {"2520", 2, true}, {"2521", 2, true},
	{"2530", 2, true}, {"2531", 2, true},
	{"2540", 2, true}, {"2600", 2, true}, {"2700", 2, true},
	{"2800", 999, true}, {"2900", 999, true},
	{"3000", 2, true}, {"3001", 2, true}, {"3002", 2, true}, {"3003", 2, true},
	{"3004", 2, true}, {"3005", 2, true}, {"3006", 2, true}, {"3007", 2, true},
	{"3008", 2, true}, {"3009", 2, true}, {"3010", 2, true}, {"3011", 2, true},
	{"3012", 2, true}, {"3013", 2, true}, {"3014", 2, true}, {"3015", 2, true},
}

// tutorialCompletionV4 is the set installed by migration 4. It identifies
// profiles whose skip-tutorial setting was enabled before migration 5.
var tutorialCompletionV4 = []TutorialStep{
	{"1000", 999, true}, {"1010", 2, true}, {"1011", 2, true},
	{"2000", 3, true}, {"2001", 5, true}, {"2002", 1, true}, {"2003", 1, true},
	{"2140", 6, true}, {"2160", 5, true}, {"2400", 2, true},
	{"2500", 2, true}, {"2510", 2, true}, {"2530", 2, true}, {"2531", 2, true},
	{"2540", 2, true}, {"2600", 2, true}, {"2700", 2, true},
	{"2800", 999, true}, {"2900", 999, true},
	{"3000", 2, true}, {"3001", 2, true}, {"3002", 2, true}, {"3003", 2, true},
	{"3004", 2, true}, {"3005", 2, true}, {"3006", 2, true},
}

// previousCompletedTutorial is the set used before migration 4. Only profiles
// that already contained this whole set were explicitly marked complete.
var previousCompletedTutorial = []TutorialStep{
	{"1000", 999, true}, {"1010", 2, true}, {"1011", 2, true},
	{"2000", 3, true}, {"2002", 1, true}, {"2003", 1, true},
	{"2140", 6, true}, {"2400", 2, true},
	{"3000", 2, true}, {"3001", 2, true}, {"3002", 2, true}, {"3003", 2, true},
	{"3004", 2, true}, {"3005", 2, true}, {"3006", 2, true},
}

func hasCompletedTutorial(progress, required []TutorialStep) bool {
	completed := make(map[string]int64, len(progress))
	for _, value := range progress {
		if value.Completed && value.Step > completed[value.TutorialID] {
			completed[value.TutorialID] = value.Step
		}
	}
	for _, value := range required {
		if completed[value.TutorialID] < value.Step {
			return false
		}
	}
	return len(required) > 0
}

func setTutorialTx(ctx context.Context, tx *sql.Tx, playerID string, complete bool, completions []TutorialStep, now int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM tutorial_progress WHERE player_id=?`, playerID); err != nil {
		return fmt.Errorf("reset tutorial: %w", err)
	}
	steps := []TutorialStep{{"1000", 0, false}}
	if complete {
		if len(completions) == 0 {
			return fmt.Errorf("set tutorial: completed progress is empty")
		}
		steps = completions
	}
	seen := make(map[string]bool, len(steps))
	for _, step := range steps {
		if step.TutorialID == "" || step.Step < 0 || seen[step.TutorialID] {
			return fmt.Errorf("set tutorial: invalid or duplicate tutorial %q", step.TutorialID)
		}
		seen[step.TutorialID] = true
		step.Completed = complete
		if _, err := tx.ExecContext(ctx, `INSERT INTO tutorial_progress(player_id,tutorial_id,step,completed,updated_at) VALUES(?,?,?,?,?)`, playerID, step.TutorialID, step.Step, step.Completed, now); err != nil {
			return fmt.Errorf("set tutorial: %w", err)
		}
	}
	return nil
}

func (s *Store) SelectActive(ctx context.Context, deviceAccount, playerID string) error {
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin select active: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO devices(device_account,created_at,last_seen_at) VALUES(?,?,?) ON CONFLICT(device_account) DO UPDATE SET last_seen_at=excluded.last_seen_at`, deviceAccount, now, now); err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM players WHERE player_id=?`, playerID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_players(device_account,player_id,linked_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, deviceAccount, playerID, now); err != nil {
		return err
	}
	var previous string
	previousErr := tx.QueryRowContext(ctx, `SELECT player_id FROM active_profiles WHERE device_account=?`, deviceAccount).Scan(&previous)
	if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
		return previousErr
	}
	if previous == playerID {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO active_profiles(device_account,player_id,selected_at) VALUES(?,?,?) ON CONFLICT(device_account) DO UPDATE SET player_id=excluded.player_id,selected_at=excluded.selected_at`, deviceAccount, playerID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_account=? AND revoked_at IS NULL`, now, deviceAccount); err != nil {
		return fmt.Errorf("revoke device sessions: %w", err)
	}
	return tx.Commit()
}

func (s *Store) RevokeDeviceSessions(ctx context.Context, deviceAccount string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_account=? AND revoked_at IS NULL`, s.now().UTC().Unix(), deviceAccount)
	if err != nil {
		return fmt.Errorf("revoke device sessions: %w", err)
	}
	return nil
}

// SelectPending saves the account to bind on the first device login.
func (s *Store) SelectPending(ctx context.Context, playerID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pending_profile(singleton,player_id) VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET player_id=excluded.player_id`, playerID)
	return err
}

func (s *Store) CurrentDevice(ctx context.Context) (Device, error) {
	var d Device
	var seen, authorized sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT d.device_account,d.last_seen_at,d.last_authorized_at,COALESCE(a.player_id,''),COALESCE(d.authorized_player_id,'') FROM devices d LEFT JOIN active_profiles a ON a.device_account=d.device_account ORDER BY CASE WHEN d.last_authorized_at IS NULL THEN 1 ELSE 0 END,d.last_authorized_at DESC,d.last_seen_at DESC LIMIT 1`).Scan(&d.Account, &seen, &authorized, &d.ActivePlayerID, &d.AuthorizedPlayerID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := s.db.QueryRowContext(ctx, `SELECT player_id FROM pending_profile WHERE singleton=1`).Scan(&d.ActivePlayerID); err == nil {
			return d, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Device{}, err
		}
		return Device{}, fmt.Errorf("%w: no local device", ErrNotFound)
	}
	if err != nil {
		return Device{}, fmt.Errorf("load current device: %w", err)
	}
	d.LastSeenAt = time.Unix(seen.Int64, 0).UTC()
	if authorized.Valid {
		d.LastAuthorizedAt = time.Unix(authorized.Int64, 0).UTC()
	}
	return d, nil
}

func (s *Store) NewDeviceSession(ctx context.Context, deviceAccount, playerID string, lifetime time.Duration) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin session: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_account=? AND revoked_at IS NULL`, now.Unix(), deviceAccount); err != nil {
		return "", fmt.Errorf("revoke sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,player_id,device_account,created_at,expires_at) VALUES(?,?,?,?,?)`, tokenHash(token), playerID, deviceAccount, now.Unix(), now.Add(lifetime).Unix()); err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit session: %w", err)
	}
	return token, nil
}

// NewSession remains as a compatibility helper for existing callers/tests.
func (s *Store) NewSession(ctx context.Context, playerID string, lifetime time.Duration) (string, error) {
	var device string
	if err := s.db.QueryRowContext(ctx, `SELECT device_account FROM active_profiles WHERE player_id=? ORDER BY selected_at DESC LIMIT 1`, playerID).Scan(&device); err != nil {
		return "", fmt.Errorf("resolve session device: %w", err)
	}
	return s.NewDeviceSession(ctx, device, playerID, lifetime)
}

func (s *Store) PlayerBySession(ctx context.Context, token string) (Player, error) {
	if token == "" {
		return Player{}, ErrInvalidSession
	}
	row := s.db.QueryRowContext(ctx, `SELECT p.player_id,p.display_name,p.level,p.experience,p.state_version,p.created_at,p.updated_at,s.device_account FROM sessions s JOIN players p ON p.player_id=s.player_id JOIN active_profiles a ON a.device_account=s.device_account AND a.player_id=s.player_id WHERE s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>?`, tokenHash(token), s.now().UTC().Unix())
	var p Player
	var created, updated int64
	err := row.Scan(&p.ID, &p.DisplayName, &p.Level, &p.Experience, &p.StateVersion, &created, &updated, &p.DeviceAccount)
	if errors.Is(err, sql.ErrNoRows) {
		return Player{}, ErrInvalidSession
	}
	if err != nil {
		return Player{}, fmt.Errorf("resolve session: %w", err)
	}
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	return p, nil
}

func (s *Store) RememberIdempotency(ctx context.Context, playerID, method, key string, response []byte) ([]byte, bool, error) {
	if key == "" {
		return nil, false, fmt.Errorf("idempotency key is required")
	}
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO idempotency_keys(player_id,method,key,response,created_at) VALUES(?,?,?,?,?)`, playerID, method, key, response, s.now().UTC().Unix())
	if err != nil {
		return nil, false, fmt.Errorf("store idempotency result: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return append([]byte(nil), response...), false, nil
	}
	var existing []byte
	if err := s.db.QueryRowContext(ctx, `SELECT response FROM idempotency_keys WHERE player_id=? AND method=? AND key=?`, playerID, method, key).Scan(&existing); err != nil {
		return nil, false, err
	}
	return existing, true, nil
}

type rowScanner interface{ Scan(...any) error }

func scanPlayer(row rowScanner) (Player, error) {
	var p Player
	var created, updated int64
	err := row.Scan(&p.ID, &p.DisplayName, &p.Level, &p.Experience, &p.StateVersion, &created, &updated)
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	return p, err
}
func initialPlayer(device string, now time.Time) Player {
	return Player{ID: PlayerID(device), DisplayName: "Local Player", Level: 1, Experience: 0, StateVersion: 1, CreatedAt: now, UpdatedAt: now}
}

func PlayerID(deviceAccount string) string {
	sum := sha256.Sum256([]byte("ptcgp-local-player:" + deviceAccount))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
func BaaSUserID(deviceAccount string) string {
	sum := sha256.Sum256([]byte("ptcgp-local-baas:" + deviceAccount))
	return hex.EncodeToString(sum[:8])
}
func SupportID(playerID string) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const supportIDSpace = uint64(839_299_365_868_340_224) // 62^10

	sum := sha256.Sum256([]byte("ptcgp-local-support:" + playerID))
	value := binary.BigEndian.Uint64(sum[:8]) % supportIDSpace
	encoded := [10]byte{}
	for index := len(encoded) - 1; index >= 0; index-- {
		encoded[index] = alphabet[value%uint64(len(alphabet))]
		value /= uint64(len(alphabet))
	}
	return string(encoded[:])
}
func FriendID(playerID string) string {
	sum := sha256.Sum256([]byte("ptcgp-local-friend:" + playerID))
	value := binary.BigEndian.Uint64(sum[:8]) % 10_000_000_000_000_000
	return fmt.Sprintf("%04d-%04d-%04d-%04d", value/1_000_000_000_000, value/100_000_000%10_000, value/10_000%10_000, value%10_000)
}
func randomUUID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}
func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func tokenHash(token string) []byte { sum := sha256.Sum256([]byte(token)); return sum[:] }
