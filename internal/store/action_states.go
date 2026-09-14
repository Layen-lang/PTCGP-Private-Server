package store

import (
	"context"
	"database/sql"
	"time"
)

type ActionState struct {
	Key       int32
	Target    string
	Date      time.Time
	Count     int64
	UpdatedAt time.Time
}

type ActionTotal struct {
	Key    int32
	Target string
	Count  int64
}

func (s *Store) AddAction(ctx context.Context, playerID string, key int32, target string, count int64, dated bool) error {
	if key <= 0 || count <= 0 {
		return nil
	}
	now := s.now().UTC()
	date := ""
	if dated {
		date = gameDayStart(now).Format("2006-01-02")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var today int64
	err = tx.QueryRowContext(ctx, `SELECT count FROM player_action_states WHERE player_id=? AND action_key=? AND action_target=? AND action_date=?`, playerID, key, target, date).Scan(&today)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		today += count
	} else {
		var historical int64
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(count),0) FROM player_action_states WHERE player_id=? AND action_key=? AND action_target=?`, playerID, key, target).Scan(&historical)
		if err != nil {
			return err
		}
		today = historical + count
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO player_action_states(player_id,action_key,action_target,action_date,count,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(player_id,action_key,action_target,action_date) DO UPDATE SET count=excluded.count,updated_at=excluded.updated_at`, playerID, key, target, date, today, now.Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ProjectActionTotals writes monotonic cumulative snapshots. It is used to
// backfill counters from authoritative inventory data and never decreases a
// total when cards are traded away or consumed.
func (s *Store) ProjectActionTotals(ctx context.Context, playerID string, totals []ActionTotal) error {
	if len(totals) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	date := gameDayStart(now).Format("2006-01-02")
	for _, total := range totals {
		if total.Key <= 0 || total.Count <= 0 {
			continue
		}
		var latest int64
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(count),0) FROM player_action_states WHERE player_id=? AND action_key=? AND action_target=?`, playerID, total.Key, total.Target).Scan(&latest)
		if err != nil {
			return err
		}
		if total.Count <= latest {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO player_action_states(player_id,action_key,action_target,action_date,count,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(player_id,action_key,action_target,action_date) DO UPDATE SET count=excluded.count,updated_at=excluded.updated_at`, playerID, total.Key, total.Target, date, total.Count, now.Unix())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LatestActionTotals(ctx context.Context, playerID string) ([]ActionTotal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.action_key,a.action_target,a.count FROM player_action_states a WHERE a.player_id=? AND NOT EXISTS (SELECT 1 FROM player_action_states b WHERE b.player_id=a.player_id AND b.action_key=a.action_key AND b.action_target=a.action_target AND (b.action_date>a.action_date OR (b.action_date=a.action_date AND b.updated_at>a.updated_at))) ORDER BY a.action_key,a.action_target`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ActionTotal
	for rows.Next() {
		var total ActionTotal
		if err := rows.Scan(&total.Key, &total.Target, &total.Count); err != nil {
			return nil, err
		}
		result = append(result, total)
	}
	return result, rows.Err()
}

func (s *Store) ActionStates(ctx context.Context, playerID string, modifiedAfter time.Time, offset, limit int) ([]ActionState, bool, time.Time, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT action_key,action_target,action_date,count,updated_at FROM player_action_states WHERE player_id=? AND updated_at>? ORDER BY action_key,action_target,action_date,updated_at LIMIT ? OFFSET ?`, playerID, modifiedAfter.UTC().Unix(), limit+1, offset)
	if err != nil {
		return nil, false, time.Time{}, err
	}
	defer rows.Close()
	result := make([]ActionState, 0, limit+1)
	var lastModified time.Time
	for rows.Next() {
		var value ActionState
		var date string
		var updatedAt int64
		if err := rows.Scan(&value.Key, &value.Target, &date, &value.Count, &updatedAt); err != nil {
			return nil, false, time.Time{}, err
		}
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if date != "" {
			value.Date, _ = time.Parse("2006-01-02", date)
		} else {
			// ActionState.date is required by the client. Older local records were
			// mistakenly stored without it, so recover their game day from the
			// update time while they age out of the database naturally.
			value.Date = gameDayStart(value.UpdatedAt)
		}
		if value.UpdatedAt.After(lastModified) {
			lastModified = value.UpdatedAt
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, time.Time{}, err
	}
	hasNext := len(result) > limit
	if hasNext {
		result = result[:limit]
	}
	return result, hasNext, lastModified, nil
}
