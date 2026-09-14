package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	TradeSessionWaiting   = 1
	TradeSessionAnswered  = 2
	TradeSessionConfirmed = 3
	TradeSessionRejected  = 4
	tradeSessionLifetime  = 48 * time.Hour
)

type TradeCard struct {
	CardID, ExpansionID string
	Language            int32
	LastAmount          int64
}

type TradeSession struct {
	ID, ProposerPlayerID, PartnerPlayerID string
	ProposerCard, PartnerCard             TradeCard
	ProposerDeposit, PartnerDeposit       []byte
	ProposerMessageStanceID               string
	ProposerMessageLanguage               int32
	State                                 int
	ProposerReceived, PartnerReceived     bool
	CreatedAt, UpdatedAt, ExpireAt        time.Time
	CompletedAt                           time.Time
}

type SubmitTradeInput struct {
	ProposerPlayerID, PartnerPlayerID string
	Card                              TradeCard
	Deposit                           []byte
	MessageStanceID                   string
	MessageLanguage                   int32
}

func (s *Store) SubmitTrade(ctx context.Context, input SubmitTradeInput) (TradeSession, TradeState, error) {
	if input.ProposerPlayerID == input.PartnerPlayerID || input.Card.CardID == "" || input.Card.ExpansionID == "" {
		return TradeSession{}, TradeState{}, fmt.Errorf("%w: invalid trade proposal", ErrRuleViolation)
	}
	proposerState, err := s.TradeState(ctx, input.ProposerPlayerID)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	partnerState, err := s.TradeState(ctx, input.PartnerPlayerID)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	if partnerState.BlockAll {
		return TradeSession{}, TradeState{}, fmt.Errorf("%w: partner blocks trades", ErrRuleViolation)
	}
	if proposerState.Power < 1 {
		return TradeSession{}, TradeState{}, fmt.Errorf("%w: trade power", ErrInsufficientResources)
	}
	sessionID, err := randomUUID()
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	defer tx.Rollback()
	if err := requireMatureFriends(ctx, tx, input.ProposerPlayerID, input.PartnerPlayerID, now); err != nil {
		return TradeSession{}, TradeState{}, err
	}
	for _, playerID := range []string{input.ProposerPlayerID, input.PartnerPlayerID} {
		active, err := activeTradeCount(ctx, tx, playerID)
		if err != nil {
			return TradeSession{}, TradeState{}, err
		}
		if active != 0 {
			return TradeSession{}, TradeState{}, fmt.Errorf("%w: player already has an active trade", ErrRuleViolation)
		}
	}
	remaining, err := reserveTradeCard(ctx, tx, input.ProposerPlayerID, input.Card.CardID, now)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_trade_state SET power=power-1 WHERE player_id=? AND power>=1`, input.ProposerPlayerID); err != nil {
		return TradeSession{}, TradeState{}, err
	}
	expireAt := now.Add(tradeSessionLifetime)
	if _, err := tx.ExecContext(ctx, `INSERT INTO trade_sessions(session_id,proposer_player_id,partner_player_id,proposer_card_id,proposer_expansion_id,proposer_language,proposer_card_last_amount,proposer_deposit,proposer_message_stance_id,proposer_message_language,state,created_at,updated_at,expire_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, sessionID, input.ProposerPlayerID, input.PartnerPlayerID, input.Card.CardID, input.Card.ExpansionID, input.Card.Language, remaining, input.Deposit, input.MessageStanceID, input.MessageLanguage, TradeSessionWaiting, now.Unix(), now.Unix(), expireAt.Unix()); err != nil {
		return TradeSession{}, TradeState{}, err
	}
	if err := tx.Commit(); err != nil {
		return TradeSession{}, TradeState{}, err
	}
	proposerState.Power--
	return TradeSession{ID: sessionID, ProposerPlayerID: input.ProposerPlayerID, PartnerPlayerID: input.PartnerPlayerID, ProposerCard: TradeCard{CardID: input.Card.CardID, ExpansionID: input.Card.ExpansionID, Language: input.Card.Language, LastAmount: remaining}, ProposerDeposit: append([]byte(nil), input.Deposit...), ProposerMessageStanceID: input.MessageStanceID, ProposerMessageLanguage: input.MessageLanguage, State: TradeSessionWaiting, CreatedAt: now, UpdatedAt: now, ExpireAt: expireAt}, proposerState, nil
}

func (s *Store) AcceptTrade(ctx context.Context, playerID, sessionID string, card TradeCard, deposit []byte) (TradeSession, TradeState, error) {
	if sessionID == "" || card.CardID == "" || card.ExpansionID == "" {
		return TradeSession{}, TradeState{}, fmt.Errorf("%w: invalid trade answer", ErrRuleViolation)
	}
	power, err := s.TradeState(ctx, playerID)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	if power.Power < 1 {
		return TradeSession{}, TradeState{}, fmt.Errorf("%w: trade power", ErrInsufficientResources)
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	defer tx.Rollback()
	session, err := loadTradeSession(ctx, tx, sessionID)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	if session.PartnerPlayerID != playerID || session.State != TradeSessionWaiting || !now.Before(session.ExpireAt) {
		return TradeSession{}, TradeState{}, fmt.Errorf("%w: proposal is not answerable", ErrRuleViolation)
	}
	remaining, err := reserveTradeCard(ctx, tx, playerID, card.CardID, now)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_trade_state SET power=power-1 WHERE player_id=? AND power>=1`, playerID); err != nil {
		return TradeSession{}, TradeState{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE trade_sessions SET partner_card_id=?,partner_expansion_id=?,partner_language=?,partner_card_last_amount=?,partner_deposit=?,state=?,updated_at=? WHERE session_id=? AND state=?`, card.CardID, card.ExpansionID, card.Language, remaining, deposit, TradeSessionAnswered, now.Unix(), sessionID, TradeSessionWaiting)
	if err != nil {
		return TradeSession{}, TradeState{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return TradeSession{}, TradeState{}, fmt.Errorf("%w: proposal changed", ErrRuleViolation)
	}
	if err := tx.Commit(); err != nil {
		return TradeSession{}, TradeState{}, err
	}
	session.PartnerCard = TradeCard{CardID: card.CardID, ExpansionID: card.ExpansionID, Language: card.Language, LastAmount: remaining}
	session.PartnerDeposit = append([]byte(nil), deposit...)
	session.State = TradeSessionAnswered
	session.UpdatedAt = now
	power.Power--
	return session, power, nil
}

func (s *Store) ConfirmTrade(ctx context.Context, playerID, sessionID string) (TradeSession, error) {
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE trade_sessions SET state=?,updated_at=?,completed_at=? WHERE session_id=? AND proposer_player_id=? AND state=? AND expire_at>?`, TradeSessionConfirmed, now.Unix(), now.Unix(), sessionID, playerID, TradeSessionAnswered, now.Unix())
	if err != nil {
		return TradeSession{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return TradeSession{}, fmt.Errorf("%w: trade is not confirmable", ErrRuleViolation)
	}
	return s.TradeSession(ctx, sessionID)
}

func (s *Store) RejectTrade(ctx context.Context, playerID, sessionID string) (TradeSession, error) {
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE trade_sessions SET state=?,updated_at=?,completed_at=? WHERE session_id=? AND state IN (?,?) AND (proposer_player_id=? OR partner_player_id=?)`, TradeSessionRejected, now.Unix(), now.Unix(), sessionID, TradeSessionWaiting, TradeSessionAnswered, playerID, playerID)
	if err != nil {
		return TradeSession{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return TradeSession{}, fmt.Errorf("%w: trade is not rejectable", ErrRuleViolation)
	}
	return s.TradeSession(ctx, sessionID)
}

func (s *Store) ReceiveTradeOutcome(ctx context.Context, playerID, sessionID string) (TradeSession, TradeCard, error) {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TradeSession{}, TradeCard{}, err
	}
	defer tx.Rollback()
	session, err := loadTradeSession(ctx, tx, sessionID)
	if err != nil {
		return TradeSession{}, TradeCard{}, err
	}
	var received bool
	var outcome TradeCard
	var column string
	switch playerID {
	case session.ProposerPlayerID:
		received, outcome, column = session.ProposerReceived, session.PartnerCard, "proposer_received"
	case session.PartnerPlayerID:
		received, outcome, column = session.PartnerReceived, session.ProposerCard, "partner_received"
	default:
		return TradeSession{}, TradeCard{}, fmt.Errorf("%w: trade participant", ErrRuleViolation)
	}
	if session.State != TradeSessionConfirmed || outcome.CardID == "" {
		return TradeSession{}, TradeCard{}, fmt.Errorf("%w: trade outcome is not ready", ErrRuleViolation)
	}
	if !received {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+1,last_received_at=excluded.last_received_at`, playerID, outcome.CardID, 1, now.Unix(), now.Unix()); err != nil {
			return TradeSession{}, TradeCard{}, err
		}
		query := fmt.Sprintf("UPDATE trade_sessions SET %s=1,updated_at=? WHERE session_id=?", column)
		if _, err := tx.ExecContext(ctx, query, now.Unix(), sessionID); err != nil {
			return TradeSession{}, TradeCard{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now.Unix(), playerID); err != nil {
			return TradeSession{}, TradeCard{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return TradeSession{}, TradeCard{}, err
	}
	if playerID == session.ProposerPlayerID {
		session.ProposerReceived = true
	} else {
		session.PartnerReceived = true
	}
	return session, outcome, nil
}

func (s *Store) ReceiveTradeDeposit(ctx context.Context, playerID, sessionID string) (TradeSession, TradeCard, []byte, TradeState, error) {
	state, err := s.TradeState(ctx, playerID)
	if err != nil {
		return TradeSession{}, TradeCard{}, nil, TradeState{}, err
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TradeSession{}, TradeCard{}, nil, TradeState{}, err
	}
	defer tx.Rollback()
	session, err := loadTradeSession(ctx, tx, sessionID)
	if err != nil {
		return TradeSession{}, TradeCard{}, nil, TradeState{}, err
	}
	var received bool
	var ownCard TradeCard
	var deposit []byte
	var column string
	switch playerID {
	case session.ProposerPlayerID:
		received, ownCard, deposit, column = session.ProposerReceived, session.ProposerCard, session.ProposerDeposit, "proposer_received"
	case session.PartnerPlayerID:
		received, ownCard, deposit, column = session.PartnerReceived, session.PartnerCard, session.PartnerDeposit, "partner_received"
	default:
		return TradeSession{}, TradeCard{}, nil, TradeState{}, fmt.Errorf("%w: trade participant", ErrRuleViolation)
	}
	if session.State != TradeSessionRejected || ownCard.CardID == "" {
		return TradeSession{}, TradeCard{}, nil, TradeState{}, fmt.Errorf("%w: trade deposit is not ready", ErrRuleViolation)
	}
	if !received {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+1,last_received_at=excluded.last_received_at`, playerID, ownCard.CardID, 1, now.Unix(), now.Unix()); err != nil {
			return TradeSession{}, TradeCard{}, nil, TradeState{}, err
		}
		query := fmt.Sprintf("UPDATE trade_sessions SET %s=1,updated_at=? WHERE session_id=?", column)
		if _, err := tx.ExecContext(ctx, query, now.Unix(), sessionID); err != nil {
			return TradeSession{}, TradeCard{}, nil, TradeState{}, err
		}
		if state.Power < tradePowerLimit {
			state.Power++
			if _, err := tx.ExecContext(ctx, `UPDATE player_trade_state SET power=? WHERE player_id=?`, state.Power, playerID); err != nil {
				return TradeSession{}, TradeCard{}, nil, TradeState{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return TradeSession{}, TradeCard{}, nil, TradeState{}, err
	}
	return session, ownCard, append([]byte(nil), deposit...), state, nil
}

func (s *Store) ActiveTradeSession(ctx context.Context, playerID string) (TradeSession, error) {
	now := s.now().UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE trade_sessions SET state=?,updated_at=?,completed_at=? WHERE state IN (?,?) AND expire_at<=?`, TradeSessionRejected, now.Unix(), now.Unix(), TradeSessionWaiting, TradeSessionAnswered, now.Unix()); err != nil {
		return TradeSession{}, err
	}
	return loadTradeSessionQuery(ctx, s.db, `SELECT `+tradeSessionColumns+` FROM trade_sessions WHERE (proposer_player_id=? AND (state IN (?,?) OR (state IN (?,?) AND proposer_received=0))) OR (partner_player_id=? AND (state IN (?,?) OR (state IN (?,?) AND partner_received=0))) ORDER BY updated_at DESC LIMIT 1`, playerID, TradeSessionWaiting, TradeSessionAnswered, TradeSessionConfirmed, TradeSessionRejected, playerID, TradeSessionWaiting, TradeSessionAnswered, TradeSessionConfirmed, TradeSessionRejected)
}

func (s *Store) TradeSession(ctx context.Context, sessionID string) (TradeSession, error) {
	return loadTradeSession(ctx, s.db, sessionID)
}

func (s *Store) TradeHistories(ctx context.Context, playerID string, limit, offset int) ([]TradeSession, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+tradeSessionColumns+` FROM trade_sessions WHERE state=? AND (proposer_player_id=? OR partner_player_id=?) ORDER BY completed_at DESC LIMIT ? OFFSET ?`, TradeSessionConfirmed, playerID, playerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []TradeSession
	for rows.Next() {
		session, err := scanTradeSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func activeTradeCount(ctx context.Context, tx *sql.Tx, playerID string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM trade_sessions WHERE (proposer_player_id=? AND (state IN (?,?) OR (state IN (?,?) AND proposer_received=0))) OR (partner_player_id=? AND (state IN (?,?) OR (state IN (?,?) AND partner_received=0)))`, playerID, TradeSessionWaiting, TradeSessionAnswered, TradeSessionConfirmed, TradeSessionRejected, playerID, TradeSessionWaiting, TradeSessionAnswered, TradeSessionConfirmed, TradeSessionRejected).Scan(&count)
	return count, err
}

func reserveTradeCard(ctx context.Context, tx *sql.Tx, playerID, cardID string, now time.Time) (int64, error) {
	var amount int64
	if err := tx.QueryRowContext(ctx, `SELECT quantity FROM player_cards WHERE player_id=? AND card_id=?`, playerID, cardID).Scan(&amount); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrInsufficientResources
	} else if err != nil {
		return 0, err
	}
	if amount < 2 {
		return 0, fmt.Errorf("%w: trader must retain one copy", ErrInsufficientResources)
	}
	remaining := amount - 1
	if _, err := tx.ExecContext(ctx, `UPDATE player_cards SET quantity=?,last_received_at=? WHERE player_id=? AND card_id=?`, remaining, now.Unix(), playerID, cardID); err != nil {
		return 0, err
	}
	return remaining, nil
}

const tradeSessionColumns = `session_id,proposer_player_id,partner_player_id,proposer_card_id,proposer_expansion_id,proposer_language,proposer_card_last_amount,proposer_deposit,proposer_message_stance_id,proposer_message_language,partner_card_id,partner_expansion_id,partner_language,partner_card_last_amount,partner_deposit,state,proposer_received,partner_received,created_at,updated_at,expire_at,completed_at`

type tradeSessionQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadTradeSession(ctx context.Context, query tradeSessionQuery, id string) (TradeSession, error) {
	return loadTradeSessionQuery(ctx, query, `SELECT `+tradeSessionColumns+` FROM trade_sessions WHERE session_id=?`, id)
}

func loadTradeSessionQuery(ctx context.Context, query tradeSessionQuery, statement string, args ...any) (TradeSession, error) {
	session, err := scanTradeSession(query.QueryRowContext(ctx, statement, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return TradeSession{}, ErrNotFound
	}
	return session, err
}

func scanTradeSession(scanner tradeStateScanner) (TradeSession, error) {
	var session TradeSession
	var partnerCardID, partnerExpansion sql.NullString
	var partnerLanguage, partnerLastAmount, completedAt sql.NullInt64
	var proposerReceived, partnerReceived int
	var createdAt, updatedAt, expireAt int64
	if err := scanner.Scan(&session.ID, &session.ProposerPlayerID, &session.PartnerPlayerID, &session.ProposerCard.CardID, &session.ProposerCard.ExpansionID, &session.ProposerCard.Language, &session.ProposerCard.LastAmount, &session.ProposerDeposit, &session.ProposerMessageStanceID, &session.ProposerMessageLanguage, &partnerCardID, &partnerExpansion, &partnerLanguage, &partnerLastAmount, &session.PartnerDeposit, &session.State, &proposerReceived, &partnerReceived, &createdAt, &updatedAt, &expireAt, &completedAt); err != nil {
		return TradeSession{}, err
	}
	if partnerCardID.Valid {
		session.PartnerCard.CardID = partnerCardID.String
	}
	if partnerExpansion.Valid {
		session.PartnerCard.ExpansionID = partnerExpansion.String
	}
	if partnerLanguage.Valid {
		session.PartnerCard.Language = int32(partnerLanguage.Int64)
	}
	if partnerLastAmount.Valid {
		session.PartnerCard.LastAmount = partnerLastAmount.Int64
	}
	session.ProposerReceived, session.PartnerReceived = proposerReceived != 0, partnerReceived != 0
	session.CreatedAt, session.UpdatedAt, session.ExpireAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC(), time.Unix(expireAt, 0).UTC()
	if completedAt.Valid {
		session.CompletedAt = time.Unix(completedAt.Int64, 0).UTC()
	}
	return session, nil
}
