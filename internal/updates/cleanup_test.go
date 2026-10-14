package updates

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
)

func TestCleanupRetainsLastBackupAndNewDownloads(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "data", "updates")
	oldKey, newKey := strings.Repeat("a", 64), strings.Repeat("b", 64)
	keepBackup := filepath.Join(base, "backup-bbbbbbbbbbbb-123")
	keepAccounts := filepath.Join(base, "accounts-before-"+newKey+".db")
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "VERSION"), []byte("1.7.5.1"))
	write(filepath.Join(keepBackup, "bin", "ptcgp-server.exe"), []byte("previous program"))
	write(keepAccounts, []byte("previous accounts"))
	obsolete := []string{"backup-aaaaaaaaaaaa-456", "accounts-before-" + oldKey + ".db", "installer-123.exe", "versions/" + oldKey, "versions/" + newKey}
	for _, path := range obsolete {
		if strings.HasPrefix(path, "versions/") || strings.HasPrefix(path, "backup-") {
			write(filepath.Join(base, filepath.FromSlash(path), "fixture"), []byte("obsolete"))
		} else {
			write(filepath.Join(base, path), []byte("obsolete"))
		}
	}
	write(filepath.Join(base, "notes.txt"), []byte("user file"))
	write(filepath.Join(root, "data", "ptcgp.db"), []byte("live accounts"))
	logTail := bytes.Repeat([]byte("tail\n"), updateLogLimit/5+1)
	write(filepath.Join(base, "installer.log"), append(bytes.Repeat([]byte("old"), updateLogLimit), logTail...))
	plan, err := planCleanup(root, "1.7.5.1", keepBackup, keepAccounts)
	if err != nil {
		t.Fatal(err)
	}
	// This download starts after the installer has taken its snapshot.
	newDownload := filepath.Join(base, "versions", strings.Repeat("c", 64), "fixture")
	write(newDownload, []byte("next release"))
	if err := provision.WriteJSON(filepath.Join(base, "cleanup.json"), plan); err != nil {
		t.Fatal(err)
	}
	if err := finishCleanup(root, plan); err != nil {
		t.Fatal(err)
	}
	for _, path := range append(obsolete, "cleanup.json") {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("obsolete artifact %s remains: %v", path, err)
		}
	}
	for path, want := range map[string]string{
		filepath.Join(keepBackup, "bin", "ptcgp-server.exe"): "previous program",
		keepAccounts: "previous accounts", newDownload: "next release",
		filepath.Join(base, "notes.txt"):        "user file",
		filepath.Join(root, "data", "ptcgp.db"): "live accounts",
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("retained file %s = %q, want %q: %v", path, data, want, err)
		}
	}
	log, err := os.ReadFile(filepath.Join(base, "installer.log"))
	if err != nil || !bytes.Equal(log, logTail[len(logTail)-updateLogLimit:]) {
		t.Fatalf("installer log did not retain its latest %d bytes: %v", updateLogLimit, err)
	}
}

func TestCleanupPreservesReusedPreparedDownload(t *testing.T) {
	root := t.TempDir()
	key := strings.Repeat("a", 64)
	base := filepath.Join(root, "data", "updates")
	stage := filepath.Join(base, "versions", key)
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.7.5.1"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := cleanupPlan{Version: "1.7.5.1", Paths: []string{"versions/" + key}}
	marker := filepath.Join(base, "cleanup.json")
	if err := provision.WriteJSON(marker, plan); err != nil {
		t.Fatal(err)
	}
	// Discovery has finished preparing a newer release in a directory that
	// was already in the old installer's snapshot.
	m := New(root, root)
	m.key, m.state = key, State{Phase: "ready", Version: "1.7.5.2"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.Cleanup(ctx)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not finish: %v", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("newly prepared download was removed: %v", err)
	}
}

func TestCleanupRejectsUnsafePlansBeforeDeleting(t *testing.T) {
	for _, path := range []string{"../../bin", "versions/../../data", "backup-arbitrary", "installer-x.exe", "accounts-before-invalid.db", "installer-1.exe/child"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, "data", "updates")
			if err := os.MkdirAll(base, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.7.5.1"), 0600); err != nil {
				t.Fatal(err)
			}
			good := filepath.Join(base, "installer-123.exe")
			if err := os.WriteFile(good, []byte("keep on validation failure"), 0600); err != nil {
				t.Fatal(err)
			}
			plan := cleanupPlan{Version: "1.7.5.1", Paths: []string{"installer-123.exe", path}}
			if err := finishCleanup(root, plan); err == nil {
				t.Fatal("unsafe plan accepted")
			}
			if _, err := os.Stat(good); err != nil {
				t.Fatalf("validation partially deleted files: %v", err)
			}
			plan = cleanupPlan{Version: "1.7.5.0", Paths: []string{"installer-123.exe"}}
			if err := finishCleanup(root, plan); err == nil {
				t.Fatal("plan for another installed version accepted")
			}
		})
	}
}
