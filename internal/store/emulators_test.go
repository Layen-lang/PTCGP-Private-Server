package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func emulatorTestStore(t *testing.T) (*Store, []Player) {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "players.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	var players []Player
	for _, name := range []string{"A", "B", "C"} {
		p, err := s.CreatePlayerWithInventory(context.Background(), name, 1, 0, false, InitialInventory{})
		if err != nil {
			t.Fatal(err)
		}
		players = append(players, p)
	}
	return s, players
}

func TestEmulatorsKeepIndependentSessionsAndAccountReservations(t *testing.T) {
	ctx := context.Background()
	s, players := emulatorTestStore(t)
	for i, serial := range []string{"MuMu", "LD9"} {
		if err := s.SelectEmulatorPlayer(ctx, serial, players[i].ID, true); err != nil {
			t.Fatal(err)
		}
	}
	a, tokenA, err := s.AuthorizeEmulator(ctx, "MuMu", "cloned-credentials", time.Hour)
	if err != nil || a.ID != players[0].ID {
		t.Fatalf("MuMu authorization = %+v, err = %v", a, err)
	}
	b, tokenB, err := s.AuthorizeEmulator(ctx, "LD9", "cloned-credentials", time.Hour)
	if err != nil || b.ID != players[1].ID {
		t.Fatalf("LD9 authorization = %+v, err = %v", b, err)
	}
	if a.DeviceAccount == b.DeviceAccount {
		t.Fatal("cloned credentials shared a device identity")
	}
	if _, err := s.PlayerBySession(ctx, tokenA); err != nil {
		t.Fatal("launching LD9 invalidated MuMu:", err)
	}
	if err := s.SelectEmulatorPlayer(ctx, "LD9", a.ID, true); !errors.Is(err, ErrPlayerInUse) {
		t.Fatalf("duplicate launch error = %v", err)
	}
	if err := s.DeletePlayer(ctx, a.ID); !errors.Is(err, ErrPlayerInUse) {
		t.Fatalf("deleting a running profile error = %v", err)
	}
	if err := s.SelectEmulatorPlayer(ctx, "MuMu", players[2].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlayerBySession(ctx, tokenA); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("old MuMu session error = %v", err)
	}
	if _, err := s.PlayerBySession(ctx, tokenB); err != nil {
		t.Fatal("changing MuMu's account invalidated LD9:", err)
	}
	if err := s.ReleaseEmulator(ctx, "MuMu"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlayerBySession(ctx, tokenB); err != nil {
		t.Fatal("Official on MuMu invalidated LD9:", err)
	}
	if err := s.SelectEmulatorPlayer(ctx, "LD9", players[2].ID, true); err != nil {
		t.Fatal("released account could not move to LD9:", err)
	}
	if err := s.ReleaseEmulator(ctx, "LD9"); err != nil {
		t.Fatal(err)
	}
	if owners, err := s.EmulatorPlayers(ctx); err != nil || len(owners) != 0 {
		t.Fatalf("reservations after stopping = %v, err = %v", owners, err)
	}
}

func TestConcurrentProcessesCannotReserveTheSameAccount(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	p, err := a.CreatePlayerWithInventory(ctx, "Shared", 1, 0, false, InitialInventory{})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var group sync.WaitGroup
	for i, state := range []*Store{a, b} {
		group.Add(1)
		go func(serial string, state *Store) {
			defer group.Done()
			<-start
			errorsCh <- state.SelectEmulatorPlayer(ctx, serial, p.ID, true)
		}([]string{"MuMu", "LD9"}[i], state)
	}
	close(start)
	group.Wait()
	close(errorsCh)
	winners, conflicts := 0, 0
	for err := range errorsCh {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrPlayerInUse) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners = %d, conflicts = %d", winners, conflicts)
	}
}

func TestEmulatorAuthorizationRequiresAnExplicitLaunch(t *testing.T) {
	s, _ := emulatorTestStore(t)
	if _, _, err := s.AuthorizeEmulator(context.Background(), "LD9", "credentials", time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unselected authorization error = %v", err)
	}
}
