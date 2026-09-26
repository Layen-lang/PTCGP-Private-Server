package provision

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

func TestDeltaPlanPreservesFullWidthSourceKeys(t *testing.T) {
	const raw = `{"outputs":[],"blobs":{"abc":{"key":18446744073709551615}}}`
	var plan imagePlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil || !bytes.Contains(encoded, []byte("18446744073709551615")) {
		t.Fatalf("source key rounded: %s (%v)", encoded, err)
	}
}

func TestGenerationIsBoundToProfileAndValidatedFiles(t *testing.T) {
	root := t.TempDir()
	cfg := configuration.Config{Path: filepath.Join(root, "server.json")}
	cfg.Client.AppVersion = "1.0.0"
	if e := os.MkdirAll(filepath.Join(root, "profiles"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"images-1.0.0.json", "image-index-1.0.0.json"} {
		if e := os.WriteFile(filepath.Join(root, "profiles", name), []byte(`{}`), 0600); e != nil {
			t.Fatal(e)
		}
	}
	key, _, _, e := profileKey(cfg, root)
	if e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(generationRoot(cfg), key)
	if e = os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(directory, "example.json")
	if e = os.WriteFile(p, []byte(`{"ok":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	record, e := fileHash(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = WriteJSON(filepath.Join(directory, "receipt.json"), receipt{Key: key, Files: map[string]fileRecord{"example.json": record}}); e != nil {
		t.Fatal(e)
	}
	if _, e = Resolve(cfg, root); e != nil {
		t.Fatal(e)
	}
	manager := New(cfg, root, root)
	if restored, restoreErr := manager.Restore(); restoreErr != nil || restored.Data.Images != filepath.Join(directory, "images", "images") || !manager.Status().Ready {
		t.Fatalf("validated generation did not restore without Android: %+v %v", manager.Status(), restoreErr)
	}
	changed := cfg
	changed.Client.MasterMemoryAladdinHash = "new-revision"
	if _, e = Resolve(changed, root); e == nil {
		t.Fatal("mixed data revisions")
	}
	if e = os.WriteFile(p, []byte(`broken`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Resolve(cfg, root); e == nil {
		t.Fatal("damaged file accepted")
	}
	manager = New(cfg, root, root)
	if _, e = manager.Restore(); e == nil || manager.Status().Ready {
		t.Fatal("damaged generation restored")
	}
}
func TestUnsafeOutputPathsRejected(t *testing.T) {
	for _, p := range []string{"../outside", "/absolute", "C:/absolute", "images\\escape", "file:stream"} {
		if _, e := safePath(t.TempDir(), p); e == nil {
			t.Errorf("accepted %q", p)
		}
	}
}
func TestAtomicJSONReplacement(t *testing.T) {
	p := filepath.Join(t.TempDir(), "active.json")
	for _, key := range []string{"old", "new"} {
		if e := WriteJSON(p, map[string]string{"key": key}); e != nil {
			t.Fatal(e)
		}
	}
	var got struct{ Key string }
	if e := readJSON(p, &got); e != nil || got.Key != "new" {
		t.Fatalf("%+v %v", got, e)
	}
}

func TestCompletedGenerationCleanupKeepsRuntimeFiles(t *testing.T) {
	root := t.TempDir()
	work := []string{"checkpoint.json", "plan.json", "config.json", "images/stream-manifest-0.json", "images/stream-manifest-7.json", "images/stream-processing.json", "images/stream-report.json", "master/master-report.json"}
	needed := []string{"receipt.json", "images/images/index.json", "images/images/card.png", "master/output/it_IT/Cards.json"}
	for _, relative := range append(work, needed...) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := cleanupGeneration(root); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range work {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Fatalf("import artifact remains: %s (%v)", relative, err)
		}
	}
	for _, relative := range needed {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("runtime file removed: %s (%v)", relative, err)
		}
	}
}
