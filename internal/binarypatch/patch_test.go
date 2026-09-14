package binarypatch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyRepeatRejectAndRollback(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "library.so")
	backup := path + ".original"
	original := []byte{0, 1, 2, 3, 4, 5}
	patched := []byte{0, 1, 9, 9, 4, 5}
	if err := os.WriteFile(path, original, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		SourceSHA256:   hashFixture(original),
		TargetSHA256:   hashFixture(patched),
		Offset:         2,
		ExpectedHex:    "0203",
		ReplacementHex: "0909",
	}
	if state, err := Apply(path, backup, manifest); err != nil || state != StatePatched {
		t.Fatalf("apply: state=%s err=%v", state, err)
	}
	if state, err := Apply(path, backup, manifest); err != nil || state != StatePatched {
		t.Fatalf("repeat: state=%s err=%v", state, err)
	}
	if err := Rollback(path, backup, manifest); err != nil {
		t.Fatal(err)
	}
	if state, err := Verify(path, manifest); err != nil || state != StateOriginal {
		t.Fatalf("verify rollback: state=%s err=%v", state, err)
	}
	if err := os.WriteFile(path, []byte("wrong"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(path, manifest); err == nil {
		t.Fatal("wrong hash accepted")
	}
}

func hashFixture(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
