package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestNicknameCooldownTracksNicknameOnly(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "nickname.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, err := state.CreatePlayer(ctx, "First", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return start }
	if err := state.UpdateNickname(ctx, player.ID, "Second", 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return start.Add(29 * 24 * time.Hour) }
	if err := state.UpdateNickname(ctx, player.ID, "TooSoon", 30*24*time.Hour); !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("cooldown error=%v", err)
	}
	state.now = func() time.Time { return start.Add(30 * 24 * time.Hour) }
	if err := state.UpdateNickname(ctx, player.ID, "Allowed", 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestLevelHistoriesAreSeededAndExtended(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "levels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, err := state.CreatePlayer(ctx, "Levels", 3, 175, true)
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.LevelHistories(ctx, player.ID)
	if err != nil || len(history) != 3 {
		t.Fatalf("initial history len=%d err=%v", len(history), err)
	}
	if err := state.UpdatePlayer(ctx, player.ID, player.DisplayName, 5, 505); err != nil {
		t.Fatal(err)
	}
	history, err = state.LevelHistories(ctx, player.ID)
	if err != nil || len(history) != 5 || history[4].Level != 5 {
		t.Fatalf("updated history=%+v err=%v", history, err)
	}
}
