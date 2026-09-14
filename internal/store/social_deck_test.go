package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLocalFriendLifecycleIsPersistentAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "social.db")
	state, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.CreatePlayer(ctx, "Aurore", 3, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.CreatePlayer(ctx, "Pierre", 4, 200, true)
	if err != nil {
		t.Fatal(err)
	}
	if FriendID(first.ID) == FriendID(second.ID) {
		t.Fatal("friend IDs must be profile-specific")
	}
	if _, err := state.SendFriendRequests(ctx, first.ID, []string{second.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	approved, err := state.SendFriendRequests(ctx, second.ID, []string{first.ID})
	if err != nil || len(approved) != 1 || approved[0] != first.ID {
		t.Fatalf("mutual request approval=%v err=%v", approved, err)
	}
	if err := state.SetFriendFavorite(ctx, first.ID, second.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := state.SetFriendFavorite(ctx, first.ID, second.ID, true); err != nil {
		t.Fatalf("favorite must be idempotent: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	snapshot, err := state.SocialSnapshot(ctx, first.ID)
	if err != nil || len(snapshot.Friends) != 1 || len(snapshot.FavoritePlayerIDs) != 1 {
		t.Fatalf("persistent social snapshot=%+v err=%v", snapshot, err)
	}
}

func TestDeckLifecyclePersistsStructuredCards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "decks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	p, err := state.CreatePlayer(ctx, "Deck test", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	created, err := state.SaveDeck(ctx, p.ID, Deck{Name: "Feu", Cards: map[string]int64{"PK_10_000010_00": 2}, EnergyTypes: []int32{3}, DisplayOrder: 2})
	if err != nil || created.ID != 1 {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	created.Name = "Feu v2"
	if _, err := state.SaveDeck(ctx, p.ID, created); err != nil {
		t.Fatalf("idempotent update: %v", err)
	}
	decks, err := state.Decks(ctx, p.ID)
	if err != nil || len(decks) != 1 || decks[0].Name != "Feu v2" || decks[0].Cards["PK_10_000010_00"] != 2 {
		t.Fatalf("decks=%+v err=%v", decks, err)
	}
}
