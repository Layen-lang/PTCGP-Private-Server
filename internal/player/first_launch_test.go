package player

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
)

func TestLaunchSelectedAccountBeforeFirstDeviceLogin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "first.db")
	state, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	masterPath := testfixture.MasterData(t)
	master, err := catalog.Open(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	m := New(state, master)
	p, err := m.Create(ctx, CreateInput{DisplayName: "Premier", Language: "fr_FR", Country: "FR", Level: 1})
	if err != nil {
		t.Fatal(err)
	}
	launcher := &launcherFake{}
	if err := m.OpenInGame(ctx, p.ID, launcher); err != nil {
		t.Fatal(err)
	}
	if launcher.calls != 1 {
		t.Fatalf("launches=%d", launcher.calls)
	}
	device, err := m.Device(ctx)
	if err != nil || device.ActivePlayerID != p.ID || device.Account != "" {
		t.Fatalf("pending account=%+v error=%v", device, err)
	}
	// A separate process sees the selection stored by the administration.
	gameState, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer gameState.Close()
	game := New(gameState, master)
	authorization, err := game.Authorize(ctx, "new-device")
	if err != nil || authorization.Created || authorization.Player.ID != p.ID {
		t.Fatalf("first login authorization=%+v error=%v", authorization, err)
	}
	other, err := game.Authorize(ctx, "another-device")
	if err != nil || other.Created || other.Player.ID != p.ID {
		t.Fatalf("existing account was not reused=%+v error=%v", other, err)
	}
}

func TestAuthorizeRequiresAnExplicitlyCreatedAccount(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(state, master).Authorize(ctx, "new-device"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("authorize without account error = %v, want ErrNotFound", err)
	}
}
