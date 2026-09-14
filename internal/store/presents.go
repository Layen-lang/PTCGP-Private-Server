package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const PresentPageSize = 100

type PresentAward struct {
	Kind, ID, SubID string
	Amount          int64
}

type Present struct {
	ID         string
	Payload    []byte
	Award      PresentAward
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ViewedAt   time.Time
	ReceivedAt time.Time
}

func (s *Store) QueuePresent(ctx context.Context, playerID string, payload []byte, award PresentAward, expiresAt time.Time) (Present, error) {
	if len(payload) == 0 || award.Amount < 0 {
		return Present{}, fmt.Errorf("%w: invalid present", ErrRuleViolation)
	}
	id, err := randomUUID()
	if err != nil {
		return Present{}, err
	}
	now := s.now().UTC()
	var expires any
	if !expiresAt.IsZero() {
		expires = expiresAt.UTC().Unix()
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO player_presents(player_id,present_id,payload,award_kind,award_id,award_sub_id,award_amount,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`, playerID, id, payload, award.Kind, award.ID, award.SubID, award.Amount, now.Unix(), expires)
	if err != nil {
		return Present{}, err
	}
	return Present{ID: id, Payload: payload, Award: award, CreatedAt: now, ExpiresAt: expiresAt.UTC()}, nil
}

func (s *Store) PendingPresents(ctx context.Context, playerID string, offset, limit int) ([]Present, error) {
	return s.presents(ctx, playerID, false, offset, limit)
}

func (s *Store) PresentHistories(ctx context.Context, playerID string, offset, limit int) ([]Present, error) {
	return s.presents(ctx, playerID, true, offset, limit)
}

func (s *Store) presents(ctx context.Context, playerID string, histories bool, offset, limit int) ([]Present, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > PresentPageSize+1 {
		limit = PresentPageSize
	}
	operator, order := "IS NULL", "created_at ASC"
	if histories {
		operator, order = "IS NOT NULL", "received_at DESC"
	}
	query := `SELECT present_id,payload,award_kind,award_id,award_sub_id,award_amount,created_at,expires_at,viewed_at,received_at FROM player_presents WHERE player_id=? AND received_at ` + operator + ` ORDER BY ` + order + `,present_id LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, playerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Present
	for rows.Next() {
		value, err := scanPresent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) HasNewPresents(ctx context.Context, playerID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM player_presents WHERE player_id=? AND received_at IS NULL AND viewed_at IS NULL`, playerID).Scan(&count)
	return count > 0, err
}

func (s *Store) MarkPresentsViewed(ctx context.Context, playerID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE player_presents SET viewed_at=? WHERE player_id=? AND received_at IS NULL AND viewed_at IS NULL`, s.now().UTC().Unix(), playerID)
	return err
}

func (s *Store) ReceivePresent(ctx context.Context, playerID, presentID string) (Present, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Present{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `SELECT present_id,payload,award_kind,award_id,award_sub_id,award_amount,created_at,expires_at,viewed_at,received_at FROM player_presents WHERE player_id=? AND present_id=?`, playerID, presentID)
	value, err := scanPresent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Present{}, ErrNotFound
	}
	if err != nil {
		return Present{}, err
	}
	if !value.ReceivedAt.IsZero() {
		return Present{}, fmt.Errorf("%w: present already received", ErrRuleViolation)
	}
	now := s.now().UTC()
	if !value.ExpiresAt.IsZero() && !now.Before(value.ExpiresAt) {
		return Present{}, fmt.Errorf("%w: present expired", ErrExpired)
	}
	if err := addPresentAward(ctx, tx, playerID, value.Award, now); err != nil {
		return Present{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE player_presents SET received_at=?,viewed_at=COALESCE(viewed_at,?) WHERE player_id=? AND present_id=? AND received_at IS NULL`, now.Unix(), now.Unix(), playerID, presentID)
	if err != nil {
		return Present{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return Present{}, fmt.Errorf("%w: present already received", ErrRuleViolation)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now.Unix(), playerID); err != nil {
		return Present{}, err
	}
	if err := tx.Commit(); err != nil {
		return Present{}, err
	}
	value.ReceivedAt = now
	return value, nil
}

func addPresentAward(ctx context.Context, tx *sql.Tx, playerID string, award PresentAward, now time.Time) error {
	if award.Amount == 0 || award.Kind == "" {
		return nil
	}
	var err error
	switch award.Kind {
	case "card":
		_, err = tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+excluded.quantity,last_received_at=excluded.last_received_at`, playerID, award.ID, award.Amount, now.Unix(), now.Unix())
		if err == nil {
			err = addCardInAccountLanguage(ctx, tx, playerID, award.ID, award.Amount)
		}
	case "currency":
		_, err = tx.ExecContext(ctx, `INSERT INTO player_currencies(player_id,currency_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,currency_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, award.ID, award.Amount)
	case "item":
		_, err = tx.ExecContext(ctx, `INSERT INTO player_items(player_id,item_kind,item_id,quantity,obtained_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,item_kind,item_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, award.SubID, award.ID, award.Amount, now.Unix())
	case "profile_decoration":
		_, err = tx.ExecContext(ctx, `INSERT INTO player_profile_decorations(player_id,decoration_id,quantity,obtained_at) VALUES(?,?,?,?) ON CONFLICT(player_id,decoration_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, award.ID, award.Amount, now.Unix())
	case "card_skin":
		_, err = tx.ExecContext(ctx, `INSERT INTO player_card_skins(player_id,card_id,skin_id,quantity) VALUES(?,?,?,?) ON CONFLICT(player_id,card_id,skin_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, award.ID, award.SubID, award.Amount)
	case "card_frame":
		_, err = tx.ExecContext(ctx, `INSERT INTO player_card_frames(player_id,card_id,frame_id,quantity) VALUES(?,?,?,?) ON CONFLICT(player_id,card_id,frame_id) DO UPDATE SET quantity=quantity+excluded.quantity`, playerID, award.ID, award.SubID, award.Amount)
	case "poke_gold":
		_, err = tx.ExecContext(ctx, `UPDATE player_pack_state SET poke_gold=poke_gold+? WHERE player_id=?`, award.Amount, playerID)
	default:
		return fmt.Errorf("%w: unsupported present award", ErrRuleViolation)
	}
	return err
}

type presentScanner interface {
	Scan(...any) error
}

func scanPresent(scanner presentScanner) (Present, error) {
	var value Present
	var created int64
	var expires, viewed, received sql.NullInt64
	err := scanner.Scan(&value.ID, &value.Payload, &value.Award.Kind, &value.Award.ID, &value.Award.SubID, &value.Award.Amount, &created, &expires, &viewed, &received)
	if err != nil {
		return Present{}, err
	}
	value.CreatedAt = time.Unix(created, 0).UTC()
	if expires.Valid {
		value.ExpiresAt = time.Unix(expires.Int64, 0).UTC()
	}
	if viewed.Valid {
		value.ViewedAt = time.Unix(viewed.Int64, 0).UTC()
	}
	if received.Valid {
		value.ReceivedAt = time.Unix(received.Int64, 0).UTC()
	}
	return value, nil
}
