package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestGiveCardAppliesOfficialEligibilityAndDailyLimitsAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "give-card.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	sender, err := state.CreatePlayer(ctx, "Sender", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := state.CreatePlayer(ctx, "Receiver", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.SendFriendRequests(ctx, sender.ID, []string{receiver.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SendFriendRequests(ctx, receiver.ID, []string{sender.ID}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetCard(ctx, sender.ID, "CARD_A", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := state.GiveCard(ctx, sender.ID, receiver.ID, "CARD_A", "EXP_A", 4); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("new accounts must remain locked, got %v", err)
	}
	maturedAt := time.Now().UTC().Add(-15 * 24 * time.Hour).Unix()
	if _, err := state.db.ExecContext(ctx, `UPDATE players SET created_at=? WHERE player_id IN (?,?)`, maturedAt, sender.ID, receiver.ID); err != nil {
		t.Fatal(err)
	}

	history, err := state.GiveCard(ctx, sender.ID, receiver.ID, "CARD_A", "EXP_A", 4)
	if err != nil {
		t.Fatal(err)
	}
	if history.SenderRemainingAmount != 1 {
		t.Fatalf("remaining amount=%d, want 1", history.SenderRemainingAmount)
	}
	senderSnapshot, err := state.Snapshot(ctx, sender.ID)
	if err != nil {
		t.Fatal(err)
	}
	receiverSnapshot, err := state.Snapshot(ctx, receiver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if senderSnapshot.Cards[0].Quantity != 1 || receiverSnapshot.Cards[0].Quantity != 1 {
		t.Fatalf("unexpected transfer sender=%+v receiver=%+v", senderSnapshot.Cards, receiverSnapshot.Cards)
	}
	if _, err := state.GiveCard(ctx, sender.ID, receiver.ID, "CARD_A", "EXP_A", 4); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("second daily gift must fail by rule, got %v", err)
	}
}

func TestDesiredCardsAndThankRewardsPersistIdempotently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "card-social.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first, err := state.CreatePlayer(ctx, "First", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.CreatePlayer(ctx, "Second", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	wanted := []DesiredCard{{CardID: "CARD_B", DisplayOrder: 2, ProfileOrder: 1}, {CardID: "CARD_A", DisplayOrder: 1}}
	if err := state.SetDesiredCards(ctx, first.ID, wanted); err != nil {
		t.Fatal(err)
	}
	got, err := state.DesiredCards(ctx, first.ID)
	if err != nil || len(got) != 2 || got[0].CardID != "CARD_A" {
		t.Fatalf("desired cards=%+v err=%v", got, err)
	}
	if err := state.SendThankReward(ctx, first.ID, second.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := state.SendThankReward(ctx, first.ID, second.ID, 1); err != nil {
		t.Fatalf("duplicate thank must be idempotent: %v", err)
	}
	var count int
	if err := state.db.QueryRowContext(ctx, `SELECT count(*) FROM thank_rewards`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("thank rows=%d, want 1", count)
	}
}
