package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestShowcaseLifecycleUsesSharedOwnershipAndOrdering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "showcases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	owner, err := state.CreatePlayer(ctx, "Collector", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.SaveShowcase(ctx, owner.ID, "album", Showcase{DisplayOrder: 2, PublicSetting: 1, Payload: []byte("first")})
	if err != nil || first.ID != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := state.SaveShowcase(ctx, owner.ID, "album", Showcase{DisplayOrder: 1, PublicSetting: 3, Payload: []byte("second")})
	if err != nil || second.ID != 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	values, err := state.Showcases(ctx, owner.ID, "album")
	if err != nil || len(values) != 2 || values[0].ID != second.ID {
		t.Fatalf("ordered values=%+v err=%v", values, err)
	}
	if err := state.OrderShowcases(ctx, owner.ID, "album", []ShowcaseOrder{{ID: first.ID, DisplayOrder: 0}, {ID: second.ID, DisplayOrder: 4}}); err != nil {
		t.Fatal(err)
	}
	values, _ = state.Showcases(ctx, owner.ID, "album")
	if values[0].ID != first.ID {
		t.Fatalf("reordered values=%+v", values)
	}
	if err := state.DeleteShowcase(ctx, owner.ID, "album", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Showcase(ctx, owner.ID, "album", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted showcase lookup=%v", err)
	}
}
