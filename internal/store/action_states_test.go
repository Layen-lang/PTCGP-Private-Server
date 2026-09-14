package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestActionStatesAggregateAndPaginateLikeSyncV1(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "actions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, err := state.CreatePlayer(ctx, "Actions", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1001; index++ {
		if err := state.AddAction(ctx, player.ID, 1, fmt.Sprintf("card-%04d", index), 1, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.AddAction(ctx, player.ID, 1, "card-0000", 2, false); err != nil {
		t.Fatal(err)
	}
	first, hasNext, _, err := state.ActionStates(ctx, player.ID, time.Unix(0, 0), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1000 || !hasNext {
		t.Fatalf("first page len=%d hasNext=%v", len(first), hasNext)
	}
	second, hasNext, _, err := state.ActionStates(ctx, player.ID, time.Unix(0, 0), 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || hasNext {
		t.Fatalf("second page len=%d hasNext=%v", len(second), hasNext)
	}
	foundAggregate := false
	for _, value := range append(first, second...) {
		if value.Target == "card-0000" && value.Count == 3 {
			foundAggregate = true
		}
	}
	if !foundAggregate {
		t.Fatal("aggregated action state was not returned across the two pages")
	}
	for _, value := range append(first, second...) {
		if value.Date.IsZero() {
			t.Fatalf("legacy action %q was returned without a recovered date", value.Target)
		}
	}
}

func TestDatedActionUsesSixUTCGameDay(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "dated-actions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, err := state.CreatePlayer(ctx, "Dated", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return time.Date(2026, 8, 25, 5, 59, 0, 0, time.UTC) }
	if err := state.AddAction(ctx, player.ID, 2, "", 1, true); err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC) }
	if err := state.AddAction(ctx, player.ID, 2, "", 1, true); err != nil {
		t.Fatal(err)
	}
	states, _, _, err := state.ActionStates(ctx, player.ID, time.Unix(0, 0), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("dated states=%d, want 2 across the reset", len(states))
	}
}

func TestDatedActionRemainsCumulativeAcrossGameDays(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "cumulative-actions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, err := state.CreatePlayer(ctx, "Cumulative", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC) }
	if err := state.AddAction(ctx, player.ID, 24, "", 4, true); err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return time.Date(2026, 8, 26, 6, 0, 0, 0, time.UTC) }
	if err := state.AddAction(ctx, player.ID, 24, "", 3, true); err != nil {
		t.Fatal(err)
	}
	latest, err := state.LatestActionTotals(ctx, player.ID)
	if err != nil || len(latest) != 1 || latest[0].Count != 7 {
		t.Fatalf("latest cumulative action = %+v err=%v", latest, err)
	}
}
