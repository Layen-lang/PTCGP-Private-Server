package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordAuthorizationPreservesComebackAbsenceAndCooldown(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "comeback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, _, err := state.EnsurePlayer(ctx, "device")
	if err != nil {
		t.Fatal(err)
	}
	returnedAt := time.Date(2026, 8, 15, 18, 3, 47, 0, time.UTC)
	if _, err := state.db.ExecContext(ctx, `UPDATE devices SET last_authorized_at=? WHERE device_account=?`, returnedAt.Add(-15*24*time.Hour).Unix(), "device"); err != nil {
		t.Fatal(err)
	}
	state.now = func() time.Time { return returnedAt }
	if err := state.RecordAuthorization(ctx, "device", player.ID); err != nil {
		t.Fatal(err)
	}
	info, err := state.ComebackInfo(ctx, "device")
	if err != nil || !info.IsReturning || info.AbsenceDays != 15 || !info.ReturnedAt.Equal(returnedAt) {
		t.Fatalf("first comeback=%+v err=%v", info, err)
	}

	state.now = func() time.Time { return returnedAt.Add(15 * 24 * time.Hour) }
	if err := state.RecordAuthorization(ctx, "device", player.ID); err != nil {
		t.Fatal(err)
	}
	info, err = state.ComebackInfo(ctx, "device")
	if err != nil || info.IsReturning || info.AbsenceDays != 0 {
		t.Fatalf("cooldown comeback=%+v err=%v", info, err)
	}
}
