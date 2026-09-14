package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectionLikesAreIdempotentDuringCooldown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "collections.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	liker, err := state.CreatePlayer(ctx, "Liker", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	target, err := state.CreatePlayer(ctx, "Collector", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.LikeCollection(ctx, liker.ID, target.ID); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("like without published collection=%v", err)
	}
	if err := state.SaveBestCollection(ctx, target.ID, []byte("best")); err != nil {
		t.Fatal(err)
	}
	firstExpiry, added, err := state.LikeCollection(ctx, liker.ID, target.ID)
	if err != nil || !added || !firstExpiry.Equal(now.Add(CollectionLikeCooldown)) {
		t.Fatalf("first like expiry=%v added=%v err=%v", firstExpiry, added, err)
	}
	secondExpiry, added, err := state.LikeCollection(ctx, liker.ID, target.ID)
	if err != nil || added || !secondExpiry.Equal(firstExpiry) {
		t.Fatalf("replay expiry=%v added=%v err=%v", secondExpiry, added, err)
	}
	if count, err := state.CollectionLikeCount(ctx, target.ID); err != nil || count != 1 {
		t.Fatalf("like count=%d err=%v", count, err)
	}
	now = now.Add(CollectionLikeCooldown + time.Second)
	thirdExpiry, added, err := state.LikeCollection(ctx, liker.ID, target.ID)
	if err != nil || !added || !thirdExpiry.After(firstExpiry) {
		t.Fatalf("post-cooldown expiry=%v added=%v err=%v", thirdExpiry, added, err)
	}
	if count, err := state.CollectionLikeCount(ctx, target.ID); err != nil || count != 2 {
		t.Fatalf("a new cooldown period should add a second like, count=%d err=%v", count, err)
	}
}

func TestBestCollectionCandidatesRankByLikes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "collection-candidates.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	viewer, _ := state.CreatePlayer(ctx, "Viewer", 1, 0, true)
	first, _ := state.CreatePlayer(ctx, "First", 1, 0, true)
	second, _ := state.CreatePlayer(ctx, "Second", 1, 0, true)
	third, _ := state.CreatePlayer(ctx, "Third", 1, 0, true)
	for _, value := range []Player{first, second} {
		if err := state.SaveBestCollection(ctx, value.ID, []byte(value.DisplayName)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := state.LikeCollection(ctx, third.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	values, err := state.CollectionCandidates(ctx, viewer.ID)
	if err != nil || len(values) != 2 || values[0].Player.ID != second.ID || values[0].LikeCount != 1 {
		t.Fatalf("candidates=%+v err=%v", values, err)
	}
}
