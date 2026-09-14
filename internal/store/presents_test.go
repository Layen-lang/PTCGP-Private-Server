package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPresentReceiveIsAtomicAndExactlyOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "presents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	owner, err := state.CreatePlayer(ctx, "Recipient", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	present, err := state.QueuePresent(ctx, owner.ID, []byte("protobuf"), PresentAward{Kind: "card", ID: "CARD_001", Amount: 2}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if hasNew, err := state.HasNewPresents(ctx, owner.ID); err != nil || !hasNew {
		t.Fatalf("new arrival=%v err=%v", hasNew, err)
	}
	if err := state.MarkPresentsViewed(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	if hasNew, err := state.HasNewPresents(ctx, owner.ID); err != nil || hasNew {
		t.Fatalf("viewed arrival=%v err=%v", hasNew, err)
	}
	if _, err := state.ReceivePresent(ctx, owner.ID, present.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReceivePresent(ctx, owner.ID, present.ID); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("second receive=%v", err)
	}
	snapshot, err := state.Snapshot(ctx, owner.ID)
	if err != nil || len(snapshot.Cards) != 1 || snapshot.Cards[0].CardID != "CARD_001" || snapshot.Cards[0].Quantity != 2 {
		t.Fatalf("snapshot=%+v err=%v", snapshot.Cards, err)
	}
	if pending, _ := state.PendingPresents(ctx, owner.ID, 0, 100); len(pending) != 0 {
		t.Fatalf("pending=%+v", pending)
	}
	if histories, _ := state.PresentHistories(ctx, owner.ID, 0, 100); len(histories) != 1 || histories[0].ID != present.ID {
		t.Fatalf("histories=%+v", histories)
	}
}

func TestExpiredPresentDoesNotChangeInventory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "expired-present.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	owner, _ := state.CreatePlayer(ctx, "Recipient", 1, 0, true)
	present, err := state.QueuePresent(ctx, owner.ID, []byte("protobuf"), PresentAward{Kind: "currency", ID: "CURRENCY", Amount: 5}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := state.ReceivePresent(ctx, owner.ID, present.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired receive=%v", err)
	}
	snapshot, _ := state.Snapshot(ctx, owner.ID)
	if len(snapshot.Currencies) != 0 {
		t.Fatalf("expired present changed inventory: %+v", snapshot.Currencies)
	}
}
