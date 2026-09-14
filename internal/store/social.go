package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type FriendLink struct {
	PlayerID string
	Since    time.Time
}

type FriendRequest struct {
	FromPlayerID, ToPlayerID string
	CreatedAt                time.Time
}

type SocialSnapshot struct {
	Friends           []FriendLink
	Sent, Received    []FriendRequest
	FavoritePlayerIDs []string
}

func (s *Store) SocialSnapshot(ctx context.Context, playerID string) (SocialSnapshot, error) {
	var result SocialSnapshot
	rows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN player_low_id=? THEN player_high_id ELSE player_low_id END,created_at FROM friendships WHERE player_low_id=? OR player_high_id=? ORDER BY created_at`, playerID, playerID, playerID)
	if err != nil {
		return result, fmt.Errorf("list friends: %w", err)
	}
	for rows.Next() {
		var link FriendLink
		var created int64
		if err := rows.Scan(&link.PlayerID, &created); err != nil {
			rows.Close()
			return result, err
		}
		link.Since = time.Unix(created, 0).UTC()
		result.Friends = append(result.Friends, link)
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	requestRows, err := s.db.QueryContext(ctx, `SELECT sender_player_id,receiver_player_id,created_at FROM friend_requests WHERE sender_player_id=? OR receiver_player_id=? ORDER BY created_at`, playerID, playerID)
	if err != nil {
		return result, fmt.Errorf("list friend requests: %w", err)
	}
	for requestRows.Next() {
		var request FriendRequest
		var created int64
		if err := requestRows.Scan(&request.FromPlayerID, &request.ToPlayerID, &created); err != nil {
			requestRows.Close()
			return result, err
		}
		request.CreatedAt = time.Unix(created, 0).UTC()
		if request.FromPlayerID == playerID {
			result.Sent = append(result.Sent, request)
		} else {
			result.Received = append(result.Received, request)
		}
	}
	if err := requestRows.Close(); err != nil {
		return result, err
	}
	favorites, err := s.db.QueryContext(ctx, `SELECT friend_player_id FROM friend_favorites WHERE player_id=? ORDER BY friend_player_id`, playerID)
	if err != nil {
		return result, fmt.Errorf("list favorite friends: %w", err)
	}
	defer favorites.Close()
	for favorites.Next() {
		var id string
		if err := favorites.Scan(&id); err != nil {
			return result, err
		}
		result.FavoritePlayerIDs = append(result.FavoritePlayerIDs, id)
	}
	return result, favorites.Err()
}

func (s *Store) SearchPlayers(ctx context.Context, currentPlayerID, friendID, name string) ([]Player, error) {
	players, err := s.Players(ctx)
	if err != nil {
		return nil, err
	}
	friendDigits := onlyDigits(friendID)
	name = strings.ToLower(strings.TrimSpace(name))
	var result []Player
	for _, player := range players {
		if player.ID == currentPlayerID {
			continue
		}
		if friendDigits != "" && onlyDigits(FriendID(player.ID)) != friendDigits {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(player.DisplayName), name) {
			continue
		}
		result = append(result, player)
	}
	return result, nil
}

func (s *Store) SendFriendRequests(ctx context.Context, senderID string, receiverIDs []string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	var approved []string
	for _, receiverID := range uniqueStrings(receiverIDs) {
		if senderID == receiverID {
			continue
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM players WHERE player_id=?`, receiverID).Scan(&exists); err != nil || exists == 0 {
			continue
		}
		var reverse int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM friend_requests WHERE sender_player_id=? AND receiver_player_id=?`, receiverID, senderID).Scan(&reverse); err != nil {
			return nil, err
		}
		if reverse != 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM friend_requests WHERE sender_player_id=? AND receiver_player_id=?`, receiverID, senderID); err != nil {
				return nil, err
			}
			low, high := orderedPlayers(senderID, receiverID)
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO friendships(player_low_id,player_high_id,created_at) VALUES(?,?,?)`, low, high, now); err != nil {
				return nil, err
			}
			approved = append(approved, receiverID)
			continue
		}
		low, high := orderedPlayers(senderID, receiverID)
		var alreadyFriends int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM friendships WHERE player_low_id=? AND player_high_id=?`, low, high).Scan(&alreadyFriends); err != nil {
			return nil, err
		}
		if alreadyFriends == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO friend_requests(sender_player_id,receiver_player_id,created_at) VALUES(?,?,?)`, senderID, receiverID, now); err != nil {
				return nil, err
			}
		}
	}
	return approved, tx.Commit()
}

func (s *Store) ApproveFriendRequest(ctx context.Context, receiverID, senderID string) error {
	low, high := orderedPlayers(receiverID, senderID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM friend_requests WHERE sender_player_id=? AND receiver_player_id=?`, senderID, receiverID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("%w: friend request", ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO friendships(player_low_id,player_high_id,created_at) VALUES(?,?,?)`, low, high, s.now().UTC().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RemoveFriendRequests(ctx context.Context, playerID string, otherIDs []string, sent bool) error {
	if len(otherIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, otherID := range uniqueStrings(otherIDs) {
		sender, receiver := otherID, playerID
		if sent {
			sender, receiver = playerID, otherID
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM friend_requests WHERE sender_player_id=? AND receiver_player_id=?`, sender, receiver); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteFriends(ctx context.Context, playerID string, otherIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, otherID := range uniqueStrings(otherIDs) {
		low, high := orderedPlayers(playerID, otherID)
		if _, err := tx.ExecContext(ctx, `DELETE FROM friendships WHERE player_low_id=? AND player_high_id=?`, low, high); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM friend_favorites WHERE (player_id=? AND friend_player_id=?) OR (player_id=? AND friend_player_id=?)`, playerID, otherID, otherID, playerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetFriendFavorite(ctx context.Context, playerID, friendID string, favorite bool) error {
	low, high := orderedPlayers(playerID, friendID)
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM friendships WHERE player_low_id=? AND player_high_id=?`, low, high).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("%w: friendship", ErrNotFound)
	}
	if favorite {
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO friend_favorites(player_id,friend_player_id) VALUES(?,?)`, playerID, friendID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM friend_favorites WHERE player_id=? AND friend_player_id=?`, playerID, friendID)
	return err
}

func orderedPlayers(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func onlyDigits(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
}
