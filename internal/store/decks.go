package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type Deck struct {
	ID                              int64
	Name                            string
	Cards                           map[string]int64
	EnergyTypes                     []int32
	CaseType                        int32
	ShieldID, CoinSkinID, PlayMatID string
	DisplayOrder                    int64
}

func (s *Store) Decks(ctx context.Context, playerID string) ([]Deck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT deck_id,name,display_order,energy_types,deck_case_type,deck_shield_id,coin_skin_id,play_mat_id FROM player_decks WHERE player_id=? ORDER BY display_order,deck_id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("list decks: %w", err)
	}
	var result []Deck
	for rows.Next() {
		var deck Deck
		var id, energies string
		if err := rows.Scan(&id, &deck.Name, &deck.DisplayOrder, &energies, &deck.CaseType, &deck.ShieldID, &deck.CoinSkinID, &deck.PlayMatID); err != nil {
			return nil, err
		}
		deck.ID, _ = strconv.ParseInt(id, 10, 64)
		deck.EnergyTypes = parseInt32List(energies)
		deck.Cards = make(map[string]int64)
		result = append(result, deck)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		id := strconv.FormatInt(result[index].ID, 10)
		cardRows, err := s.db.QueryContext(ctx, `SELECT card_id,quantity FROM player_deck_cards WHERE player_id=? AND deck_id=? ORDER BY card_id`, playerID, id)
		if err != nil {
			return nil, err
		}
		for cardRows.Next() {
			var cardID string
			var quantity int64
			if err := cardRows.Scan(&cardID, &quantity); err != nil {
				cardRows.Close()
				return nil, err
			}
			result[index].Cards[cardID] = quantity
		}
		if err := cardRows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) SaveDeck(ctx context.Context, playerID string, deck Deck) (Deck, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deck{}, err
	}
	defer tx.Rollback()
	if deck.ID <= 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(deck_id AS INTEGER)),0)+1 FROM player_decks WHERE player_id=?`, playerID).Scan(&deck.ID); err != nil {
			return Deck{}, err
		}
	}
	id := strconv.FormatInt(deck.ID, 10)
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_decks(player_id,deck_id,name,updated_at,display_order,energy_types,deck_case_type,deck_shield_id,coin_skin_id,play_mat_id) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(player_id,deck_id) DO UPDATE SET name=excluded.name,updated_at=excluded.updated_at,display_order=excluded.display_order,energy_types=excluded.energy_types,deck_case_type=excluded.deck_case_type,deck_shield_id=excluded.deck_shield_id,coin_skin_id=excluded.coin_skin_id,play_mat_id=excluded.play_mat_id`, playerID, id, deck.Name, s.now().UTC().Unix(), deck.DisplayOrder, formatInt32List(deck.EnergyTypes), deck.CaseType, deck.ShieldID, deck.CoinSkinID, deck.PlayMatID); err != nil {
		return Deck{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_deck_cards WHERE player_id=? AND deck_id=?`, playerID, id); err != nil {
		return Deck{}, err
	}
	for cardID, quantity := range deck.Cards {
		if quantity <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_deck_cards(player_id,deck_id,card_id,quantity) VALUES(?,?,?,?)`, playerID, id, cardID, quantity); err != nil {
			return Deck{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Deck{}, err
	}
	return deck, nil
}

func (s *Store) DeleteDeck(ctx context.Context, playerID string, deckID int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM player_decks WHERE player_id=? AND deck_id=?`, playerID, strconv.FormatInt(deckID, 10))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("%w: deck", ErrNotFound)
	}
	return nil
}

func (s *Store) ChangeDeckOrder(ctx context.Context, playerID string, orders map[int64]int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, order := range orders {
		if _, err := tx.ExecContext(ctx, `UPDATE player_decks SET display_order=? WHERE player_id=? AND deck_id=?`, order, playerID, strconv.FormatInt(id, 10)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func formatInt32List(values []int32) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.FormatInt(int64(value), 10)
	}
	return strings.Join(parts, ",")
}

func parseInt32List(value string) []int32 {
	var result []int32
	for _, part := range strings.Split(value, ",") {
		parsed, err := strconv.ParseInt(strings.TrimSpace(part), 10, 32)
		if err == nil {
			result = append(result, int32(parsed))
		}
	}
	return result
}
