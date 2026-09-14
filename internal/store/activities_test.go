package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSoloBattleClearStatesFollowFinishedWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "solo-battles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	localPlayer, err := state.CreatePlayer(ctx, "Solo", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}

	states, err := state.SoloBattleClearStates(ctx, localPlayer.ID, []string{"battle-1", "battle-2"})
	if err != nil || states["battle-1"] || states["battle-2"] {
		t.Fatalf("initial states=%v err=%v", states, err)
	}
	token, err := state.StartSoloBattle(ctx, localPlayer.ID, "battle-1")
	if err != nil {
		t.Fatal(err)
	}
	firstClear, err := state.FinishSoloBattle(ctx, localPlayer.ID, token, 1)
	if err != nil || !firstClear {
		t.Fatalf("finish firstClear=%v err=%v", firstClear, err)
	}
	states, err = state.SoloBattleClearStates(ctx, localPlayer.ID, []string{"battle-1", "battle-2"})
	if err != nil || !states["battle-1"] || states["battle-2"] {
		t.Fatalf("finished states=%v err=%v", states, err)
	}
}
