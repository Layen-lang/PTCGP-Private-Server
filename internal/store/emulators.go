package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrPlayerInUse prevents a profile from being played on two emulators.
var ErrPlayerInUse = errors.New("player is already in use on another emulator")

// EmulatorProfile remembers a selection separately from its running reservation.
type EmulatorProfile struct {
	Serial        string
	PlayerID      string
	DeviceAccount string
	Active        bool
	StartedAt     int64
}

func (s *Store) EmulatorProfile(ctx context.Context, serial string) (EmulatorProfile, error) {
	profile := EmulatorProfile{Serial: serial}
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(player_id,''),device_account,active,started_at FROM emulator_profiles WHERE serial=?`, serial).Scan(&profile.PlayerID, &profile.DeviceAccount, &profile.Active, &profile.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return profile, nil
	}
	return profile, err
}

// EmulatorDevice returns the selected profile and its confirmed authorization.
func (s *Store) EmulatorDevice(ctx context.Context, serial string) (Device, error) {
	profile, err := s.EmulatorProfile(ctx, serial)
	if err != nil {
		return Device{}, err
	}
	device := Device{Account: profile.DeviceAccount, ActivePlayerID: profile.PlayerID}
	if !profile.Active || profile.DeviceAccount == "" {
		return device, nil
	}
	var authorized sql.NullString
	var seen, loggedIn sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT authorized_player_id,last_seen_at,last_authorized_at FROM devices WHERE device_account=?`, profile.DeviceAccount).Scan(&authorized, &seen, &loggedIn)
	if errors.Is(err, sql.ErrNoRows) {
		return device, nil
	}
	if err != nil {
		return Device{}, err
	}
	if authorized.String == profile.PlayerID {
		device.AuthorizedPlayerID = authorized.String
	}
	if seen.Valid {
		device.LastSeenAt = time.Unix(seen.Int64, 0).UTC()
	}
	if loggedIn.Valid {
		device.LastAuthorizedAt = time.Unix(loggedIn.Int64, 0).UTC()
	}
	return device, nil
}

// EmulatorPlayers returns the exclusive reservations used by the administration.
func (s *Store) EmulatorPlayers(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT player_id,serial FROM emulator_profiles WHERE active=1 AND player_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var playerID, serial string
		if err := rows.Scan(&playerID, &serial); err != nil {
			return nil, err
		}
		result[playerID] = serial
	}
	return result, rows.Err()
}

// SelectEmulatorPlayer reserves atomically before restarting the game. Only the
// selected emulator's previous session is revoked; other emulators keep playing.
func (s *Store) SelectEmulatorPlayer(ctx context.Context, serial, playerID string, active bool) error {
	if serial == "" || playerID == "" {
		return fmt.Errorf("an emulator and player are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the write lock before reading reservations across server processes.
	if _, err := tx.ExecContext(ctx, `INSERT INTO emulator_profiles(serial) VALUES(?) ON CONFLICT(serial) DO UPDATE SET serial=excluded.serial`, serial); err != nil {
		return err
	}
	var owner string
	err = tx.QueryRowContext(ctx, `SELECT serial FROM emulator_profiles WHERE player_id=? AND active=1 AND serial<>?`, playerID, serial).Scan(&owner)
	if err == nil {
		return fmt.Errorf("%w: %s", ErrPlayerInUse, owner)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var account string
	if err := tx.QueryRowContext(ctx, `SELECT device_account FROM emulator_profiles WHERE serial=?`, serial).Scan(&account); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE emulator_profiles SET player_id=?,active=?,started_at=? WHERE serial=?`, playerID, active, s.now().UTC().Unix(), serial); err != nil {
		return err
	}
	if account != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_account=? AND revoked_at IS NULL`, s.now().UTC().Unix(), account); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReleaseEmulator keeps the last selection but invalidates its game sessions.
func (s *Store) ReleaseEmulator(ctx context.Context, serial string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE emulator_profiles SET active=0 WHERE serial=?`, serial); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_account IN (SELECT device_account FROM emulator_profiles WHERE serial=?) AND revoked_at IS NULL`, s.now().UTC().Unix(), serial); err != nil {
		return err
	}
	return tx.Commit()
}

// AuthorizeEmulator binds game credentials to the reservation on this emulator's
// server. Namespacing also keeps cloned Android credentials independent.
func (s *Store) AuthorizeEmulator(ctx context.Context, serial, deviceAccount string, lifetime time.Duration) (Player, string, error) {
	if serial == "" || deviceAccount == "" {
		return Player{}, "", ErrInvalidDeviceCredentials
	}
	account := "emulator:" + serial + ":" + deviceAccount
	token, err := randomToken(32)
	if err != nil {
		return Player{}, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Player{}, "", err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE emulator_profiles SET serial=serial WHERE serial=?`, serial); err != nil {
		return Player{}, "", err
	}
	var selected, previousAccount string
	if err := tx.QueryRowContext(ctx, `SELECT player_id,device_account FROM emulator_profiles WHERE serial=? AND active=1 AND player_id IS NOT NULL`, serial).Scan(&selected, &previousAccount); errors.Is(err, sql.ErrNoRows) {
		return Player{}, "", ErrNotFound
	} else if err != nil {
		return Player{}, "", err
	}
	p, err := scanPlayer(tx.QueryRowContext(ctx, `SELECT player_id,display_name,level,experience,state_version,created_at,updated_at FROM players WHERE player_id=?`, selected))
	if err != nil {
		return Player{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO devices(device_account,created_at,last_seen_at) VALUES(?,?,?) ON CONFLICT(device_account) DO UPDATE SET last_seen_at=excluded.last_seen_at`, account, now.Unix(), now.Unix()); err != nil {
		return Player{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_players(device_account,player_id,linked_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, account, selected, now.Unix()); err != nil {
		return Player{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO active_profiles(device_account,player_id,selected_at) VALUES(?,?,?) ON CONFLICT(device_account) DO UPDATE SET player_id=excluded.player_id,selected_at=excluded.selected_at`, account, selected, now.Unix()); err != nil {
		return Player{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_account IN (?,?) AND revoked_at IS NULL`, now.Unix(), account, previousAccount); err != nil {
		return Player{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE emulator_profiles SET device_account=? WHERE serial=?`, account, serial); err != nil {
		return Player{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,player_id,device_account,created_at,expires_at) VALUES(?,?,?,?,?)`, tokenHash(token), selected, account, now.Unix(), now.Add(lifetime).Unix()); err != nil {
		return Player{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Player{}, "", err
	}
	p.DeviceAccount = account
	return p, token, nil
}
