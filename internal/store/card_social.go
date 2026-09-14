package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	giveCardUnlockAge         = 14 * 24 * time.Hour
	dailyGiveCountPerFriend   = 1
	dailyGiveCardReceiveLimit = 1
	dailyThankRewardLimit     = 5
	gameDayResetHourUTC       = 6
)

var ErrRuleViolation = errors.New("game rule violation")

type DesiredCard struct {
	CardID       string
	DisplayOrder uint64
	ProfileOrder uint64
}

type GiveCardHistory struct {
	SenderPlayerID, ReceiverPlayerID string
	CardID, ExpansionID              string
	Language                         int32
	SenderRemainingAmount            int64
	GivenAt                          time.Time
}

func (s *Store) DesiredCards(ctx context.Context, playerID string) ([]DesiredCard, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT card_id,display_order,profile_order FROM player_desired_cards WHERE player_id=? ORDER BY display_order,card_id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("list desired cards: %w", err)
	}
	defer rows.Close()
	var result []DesiredCard
	for rows.Next() {
		var card DesiredCard
		if err := rows.Scan(&card.CardID, &card.DisplayOrder, &card.ProfileOrder); err != nil {
			return nil, err
		}
		result = append(result, card)
	}
	return result, rows.Err()
}

func (s *Store) SetDesiredCards(ctx context.Context, playerID string, cards []DesiredCard) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_desired_cards WHERE player_id=?`, playerID); err != nil {
		return err
	}
	for _, card := range cards {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_desired_cards(player_id,card_id,display_order,profile_order) VALUES(?,?,?,?)`, playerID, card.CardID, card.DisplayOrder, card.ProfileOrder); err != nil {
			return fmt.Errorf("save desired card %q: %w", card.CardID, err)
		}
	}
	return tx.Commit()
}

func (s *Store) GiveCard(ctx context.Context, senderID, receiverID, cardID, expansionID string, language int32) (GiveCardHistory, error) {
	if senderID == receiverID || cardID == "" || expansionID == "" {
		return GiveCardHistory{}, fmt.Errorf("%w: invalid card gift", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GiveCardHistory{}, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := requireMatureFriends(ctx, tx, senderID, receiverID, now); err != nil {
		return GiveCardHistory{}, err
	}
	dayStart := gameDayStart(now).Unix()
	var sentToFriend, receivedToday int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM give_card_histories WHERE sender_player_id=? AND receiver_player_id=? AND given_at>=?`, senderID, receiverID, dayStart).Scan(&sentToFriend); err != nil {
		return GiveCardHistory{}, err
	}
	if sentToFriend >= dailyGiveCountPerFriend {
		return GiveCardHistory{}, fmt.Errorf("%w: daily friend gift limit reached", ErrRuleViolation)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM give_card_histories WHERE receiver_player_id=? AND given_at>=?`, receiverID, dayStart).Scan(&receivedToday); err != nil {
		return GiveCardHistory{}, err
	}
	if receivedToday >= dailyGiveCardReceiveLimit {
		return GiveCardHistory{}, fmt.Errorf("%w: receiver daily gift limit reached", ErrRuleViolation)
	}
	var amount int64
	if err := tx.QueryRowContext(ctx, `SELECT quantity FROM player_cards WHERE player_id=? AND card_id=?`, senderID, cardID).Scan(&amount); errors.Is(err, sql.ErrNoRows) {
		return GiveCardHistory{}, ErrInsufficientResources
	} else if err != nil {
		return GiveCardHistory{}, err
	}
	if amount < 2 {
		return GiveCardHistory{}, fmt.Errorf("%w: sender must retain one copy", ErrInsufficientResources)
	}
	remaining := amount - 1
	if _, err := tx.ExecContext(ctx, `UPDATE player_cards SET quantity=?,last_received_at=? WHERE player_id=? AND card_id=?`, remaining, now.Unix(), senderID, cardID); err != nil {
		return GiveCardHistory{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+1,last_received_at=excluded.last_received_at`, receiverID, cardID, 1, now.Unix(), now.Unix()); err != nil {
		return GiveCardHistory{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO give_card_histories(sender_player_id,receiver_player_id,card_id,expansion_id,language,sender_remaining_amount,given_at) VALUES(?,?,?,?,?,?,?)`, senderID, receiverID, cardID, expansionID, language, remaining, now.Unix()); err != nil {
		return GiveCardHistory{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id IN (?,?)`, now.Unix(), senderID, receiverID); err != nil {
		return GiveCardHistory{}, err
	}
	if err := tx.Commit(); err != nil {
		return GiveCardHistory{}, err
	}
	return GiveCardHistory{SenderPlayerID: senderID, ReceiverPlayerID: receiverID, CardID: cardID, ExpansionID: expansionID, Language: language, SenderRemainingAmount: remaining, GivenAt: now}, nil
}

func (s *Store) GiveCardHistories(ctx context.Context, playerID string, since time.Time) ([]GiveCardHistory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sender_player_id,receiver_player_id,card_id,expansion_id,language,sender_remaining_amount,given_at FROM give_card_histories WHERE (sender_player_id=? OR receiver_player_id=?) AND given_at>=? ORDER BY given_at DESC`, playerID, playerID, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []GiveCardHistory
	for rows.Next() {
		var history GiveCardHistory
		var givenAt int64
		if err := rows.Scan(&history.SenderPlayerID, &history.ReceiverPlayerID, &history.CardID, &history.ExpansionID, &history.Language, &history.SenderRemainingAmount, &givenAt); err != nil {
			return nil, err
		}
		history.GivenAt = time.Unix(givenAt, 0).UTC()
		result = append(result, history)
	}
	return result, rows.Err()
}

func (s *Store) SendThankReward(ctx context.Context, senderID, targetID string, routeType int32) error {
	if senderID == targetID || routeType <= 0 {
		return fmt.Errorf("%w: invalid thank reward", ErrRuleViolation)
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM players WHERE player_id=?`, targetID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("%w: target player", ErrNotFound)
	}
	now := s.now().UTC()
	day := gameDay(now)
	var duplicate int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM thank_rewards WHERE sender_player_id=? AND target_player_id=? AND route_type=? AND sent_day=?`, senderID, targetID, routeType, day).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate != 0 {
		return nil
	}
	var sentToday int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM thank_rewards WHERE sender_player_id=? AND sent_day=?`, senderID, day).Scan(&sentToday); err != nil {
		return err
	}
	if sentToday >= dailyThankRewardLimit {
		return fmt.Errorf("%w: daily thank reward limit reached", ErrRuleViolation)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO thank_rewards(sender_player_id,target_player_id,route_type,sent_at,sent_day) VALUES(?,?,?,?,?)`, senderID, targetID, routeType, now.Unix(), day)
	return err
}

// Pokémon TCG Pocket changes its in-game day at 06:00 UTC, not midnight.
func gameDay(value time.Time) int64 {
	const secondsPerDay = int64(24 * time.Hour / time.Second)
	return (value.UTC().Unix() - gameDayResetHourUTC*int64(time.Hour/time.Second)) / secondsPerDay
}

func gameDayStart(value time.Time) time.Time {
	const secondsPerDay = int64(24 * time.Hour / time.Second)
	return time.Unix(gameDay(value)*secondsPerDay+gameDayResetHourUTC*int64(time.Hour/time.Second), 0).UTC()
}

func requireMatureFriends(ctx context.Context, tx *sql.Tx, firstID, secondID string, now time.Time) error {
	low, high := orderedPlayers(firstID, secondID)
	var friendship int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM friendships WHERE player_low_id=? AND player_high_id=?`, low, high).Scan(&friendship); err != nil {
		return err
	}
	if friendship == 0 {
		return fmt.Errorf("%w: players are not friends", ErrRuleViolation)
	}
	rows, err := tx.QueryContext(ctx, `SELECT created_at FROM players WHERE player_id IN (?,?)`, firstID, secondID)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var createdAt int64
		if err := rows.Scan(&createdAt); err != nil {
			return err
		}
		if now.Sub(time.Unix(createdAt, 0).UTC()) < giveCardUnlockAge {
			return fmt.Errorf("%w: card sharing is not unlocked", ErrRuleViolation)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != 2 {
		return fmt.Errorf("%w: player", ErrNotFound)
	}
	return nil
}
