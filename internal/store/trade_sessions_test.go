package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTradeSessionTransfersReservedCardsExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	proposer, partner := matureFriendsForTrade(t, ctx, state)
	if err := state.SetCard(ctx, proposer.ID, "CARD_A", 2); err != nil {
		t.Fatal(err)
	}
	if err := state.SetCard(ctx, partner.ID, "CARD_B", 2); err != nil {
		t.Fatal(err)
	}

	session, proposerPower, err := state.SubmitTrade(ctx, SubmitTradeInput{ProposerPlayerID: proposer.ID, PartnerPlayerID: partner.ID, Card: TradeCard{CardID: "CARD_A", ExpansionID: "EXP", Language: 4}, Deposit: []byte{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if proposerPower.Power != 4 || session.State != TradeSessionWaiting || session.ProposerCard.LastAmount != 1 {
		t.Fatalf("proposal session=%+v power=%+v", session, proposerPower)
	}
	if _, _, err := state.SubmitTrade(ctx, SubmitTradeInput{ProposerPlayerID: proposer.ID, PartnerPlayerID: partner.ID, Card: TradeCard{CardID: "CARD_A", ExpansionID: "EXP", Language: 4}}); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("parallel active trade must fail, got %v", err)
	}
	session, partnerPower, err := state.AcceptTrade(ctx, partner.ID, session.ID, TradeCard{CardID: "CARD_B", ExpansionID: "EXP", Language: 4}, []byte{3})
	if err != nil {
		t.Fatal(err)
	}
	if partnerPower.Power != 4 || session.State != TradeSessionAnswered || session.PartnerCard.LastAmount != 1 {
		t.Fatalf("answer session=%+v power=%+v", session, partnerPower)
	}
	if _, err := state.ConfirmTrade(ctx, partner.ID, session.ID); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("only proposer may confirm, got %v", err)
	}
	if _, err := state.ConfirmTrade(ctx, proposer.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, outcome, err := state.ReceiveTradeOutcome(ctx, proposer.ID, session.ID); err != nil || outcome.CardID != "CARD_B" {
		t.Fatalf("proposer outcome=%+v err=%v", outcome, err)
	}
	if _, outcome, err := state.ReceiveTradeOutcome(ctx, proposer.ID, session.ID); err != nil || outcome.CardID != "CARD_B" {
		t.Fatalf("duplicate receive must be idempotent: outcome=%+v err=%v", outcome, err)
	}
	if _, outcome, err := state.ReceiveTradeOutcome(ctx, partner.ID, session.ID); err != nil || outcome.CardID != "CARD_A" {
		t.Fatalf("partner outcome=%+v err=%v", outcome, err)
	}

	proposerSnapshot, err := state.Snapshot(ctx, proposer.ID)
	if err != nil {
		t.Fatal(err)
	}
	partnerSnapshot, err := state.Snapshot(ctx, partner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if quantities(proposerSnapshot)["CARD_A"] != 1 || quantities(proposerSnapshot)["CARD_B"] != 1 || quantities(partnerSnapshot)["CARD_A"] != 1 || quantities(partnerSnapshot)["CARD_B"] != 1 {
		t.Fatalf("unexpected final inventories proposer=%v partner=%v", quantities(proposerSnapshot), quantities(partnerSnapshot))
	}
	if _, err := state.ActiveTradeSession(ctx, proposer.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("completed received trade must not remain active, got %v", err)
	}
}

func matureFriendsForTrade(t *testing.T, ctx context.Context, state *Store) (Player, Player) {
	t.Helper()
	first, err := state.CreatePlayer(ctx, "Proposer", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.CreatePlayer(ctx, "Partner", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.SendFriendRequests(ctx, first.ID, []string{second.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SendFriendRequests(ctx, second.ID, []string{first.ID}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Add(-15 * 24 * time.Hour).Unix()
	if _, err := state.db.ExecContext(ctx, `UPDATE players SET created_at=? WHERE player_id IN (?,?)`, createdAt, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	return first, second
}

func quantities(snapshot Snapshot) map[string]int64 {
	result := make(map[string]int64, len(snapshot.Cards))
	for _, card := range snapshot.Cards {
		result[card.CardID] = card.Quantity
	}
	return result
}
