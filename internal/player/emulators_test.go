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

func TestEmulatorManagersAuthorizeTheCorrectAccountAndRejectForeignTokens(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "multi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	admin := New(state, master)
	a, err := admin.Create(ctx, CreateInput{DisplayName: "MuMu account", Language: "fr_FR", Country: "FR", Level: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := admin.Create(ctx, CreateInput{DisplayName: "LD9 account", Language: "fr_FR", Country: "FR", Level: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SelectEmulatorPlayer(ctx, "MuMu", a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := state.SelectEmulatorPlayer(ctx, "LD9", b.ID, true); err != nil {
		t.Fatal(err)
	}
	mumu, ld9 := NewForEmulator(state, master, "MuMu"), NewForEmulator(state, master, "LD9")
	authA, err := mumu.Authorize(ctx, "same-cloned-credentials")
	if err != nil || authA.Player.ID != a.ID {
		t.Fatalf("MuMu = %+v, err = %v", authA, err)
	}
	authB, err := ld9.Authorize(ctx, "same-cloned-credentials")
	if err != nil || authB.Player.ID != b.ID {
		t.Fatalf("LD9 = %+v, err = %v", authB, err)
	}
	if _, err := mumu.PlayerBySession(ctx, authA.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := ld9.PlayerBySession(ctx, authB.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := ld9.PlayerBySession(ctx, authA.Token); !errors.Is(err, store.ErrInvalidSession) {
		t.Fatalf("foreign token error = %v", err)
	}
	device, err := admin.Device(WithEmulator(ctx, "MuMu"))
	if err != nil || device.ActivePlayerID != a.ID || device.AuthorizedPlayerID != a.ID {
		t.Fatalf("MuMu bootstrap after LD9 login = %+v, err = %v", device, err)
	}
	if err := state.ReleaseEmulator(ctx, "MuMu"); err != nil {
		t.Fatal(err)
	}
	if _, err := ld9.PlayerBySession(ctx, authB.Token); err != nil {
		t.Fatal("Official on MuMu interrupted LD9:", err)
	}
}
