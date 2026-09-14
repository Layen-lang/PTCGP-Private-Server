package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestFeedSnoopReservesPowerAndChallengeConsumesReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	owner, err := state.CreatePlayer(ctx, "Owner", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := state.CreatePlayer(ctx, "Viewer", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	seedFeedEntry(t, state, owner.ID, "feed-1", "opening-1", now, []string{"CARD_1", "CARD_2", "CARD_3", "CARD_4", "CARD_5"})

	snooped, replay, err := state.BeginFeedSnoop(ctx, viewer.ID, "feed-1", 3)
	if err != nil || replay || snooped.Power != 2 {
		t.Fatalf("begin snoop: state=%+v replay=%v err=%v", snooped, replay, err)
	}
	snooped, replay, err = state.BeginFeedSnoop(ctx, viewer.ID, "feed-1", 3)
	if err != nil || !replay || snooped.Power != 2 {
		t.Fatalf("replay snoop: state=%+v replay=%v err=%v", snooped, replay, err)
	}
	entries, err := state.FeedEntries(ctx, viewer.ID, 16)
	if err != nil || len(entries) != 1 || !entries[0].Snooping || entries[0].ChallengedCardID != "" {
		t.Fatalf("snooping timeline: entries=%+v err=%v", entries, err)
	}

	challenged, replay, err := state.CommitFeedChallenge(ctx, viewer.ID, "feed-1", "challenge-1", "CARD_2", 3)
	if err != nil || replay || challenged.Power != 2 {
		t.Fatalf("commit challenge: state=%+v replay=%v err=%v", challenged, replay, err)
	}
	entries, err = state.FeedEntries(ctx, viewer.ID, 16)
	if err != nil || len(entries) != 1 || entries[0].Snooping || entries[0].ChallengedCardID != "CARD_2" {
		t.Fatalf("completed timeline: entries=%+v err=%v", entries, err)
	}
	profile, err := state.Snapshot(ctx, viewer.ID)
	if err != nil || len(profile.Cards) != 1 || profile.Cards[0].CardID != "CARD_2" || profile.Cards[0].Quantity != 1 {
		t.Fatalf("challenge reward: cards=%+v err=%v", profile.Cards, err)
	}
	if profile.Player.Experience != FeedChallengeExperience {
		t.Fatalf("challenge experience=%d, want %d", profile.Player.Experience, FeedChallengeExperience)
	}

	challenged, replay, err = state.CommitFeedChallenge(ctx, viewer.ID, "feed-1", "challenge-1", "CARD_2", 3)
	if err != nil || !replay || challenged.Power != 2 {
		t.Fatalf("replay challenge: state=%+v replay=%v err=%v", challenged, replay, err)
	}
}

func TestFeedSnoopRejectsAnotherActiveFeed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	owner, err := state.CreatePlayer(ctx, "Owner", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := state.CreatePlayer(ctx, "Viewer", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	seedFeedEntry(t, state, owner.ID, "feed-1", "opening-1", now, []string{"CARD_1"})
	seedFeedEntry(t, state, owner.ID, "feed-2", "opening-2", now, []string{"CARD_2"})
	if _, _, err := state.BeginFeedSnoop(ctx, viewer.ID, "feed-1", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.BeginFeedSnoop(ctx, viewer.ID, "feed-2", 1); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("second active snoop error=%v", err)
	}
}

func seedFeedEntry(t *testing.T, state *Store, ownerID, feedID, openingID string, createdAt time.Time, cards []string) {
	t.Helper()
	ctx := context.Background()
	if _, err := state.db.ExecContext(ctx, `INSERT INTO pack_openings(opening_id,player_id,transaction_id,product_id,pack_id,requested_count,returned_count,mode,seed,free_opening,power_cost,experience_reward,ceil_point_reward,shine_dust_reward,created_at) VALUES(?,?,?,?,?,1,1,'official',1,0,1,0,0,0,?)`, openingID, ownerID, "transaction-"+openingID, "product", "pack", createdAt.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO pack_opening_packs(opening_id,pack_index,pack_table_id) VALUES(?,0,'table')`, openingID); err != nil {
		t.Fatal(err)
	}
	for index, cardID := range cards {
		if _, err := state.db.ExecContext(ctx, `INSERT INTO pack_opening_cards(opening_id,pack_index,slot_index,card_id) VALUES(?,0,?,?)`, openingID, index, cardID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO feed_entries(entry_id,player_id,opening_id,created_at) VALUES(?,?,?,?)`, feedID, ownerID, openingID, createdAt.Unix()); err != nil {
		t.Fatal(err)
	}
}
