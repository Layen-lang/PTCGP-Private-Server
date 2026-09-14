package store

import (
	"context"
	"time"
)

// CardStocks is the intentionally small inventory read used by action
// projection. It avoids loading cosmetics and settings during sync.
func (s *Store) CardStocks(ctx context.Context, playerID string) ([]CardStock, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT card_id,quantity,first_received_at,last_received_at FROM player_cards WHERE player_id=?`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CardStock
	for rows.Next() {
		var value CardStock
		var first, last int64
		if err := rows.Scan(&value.CardID, &value.Quantity, &first, &last); err != nil {
			return nil, err
		}
		value.FirstReceivedAt = time.Unix(first, 0).UTC()
		value.LastReceivedAt = time.Unix(last, 0).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}
