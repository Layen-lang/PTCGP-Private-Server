package updates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReleaseConfigPreservesLocalSettings(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local.json")
	published := filepath.Join(root, "published.json")
	for path, data := range map[string]string{
		local:     `{"runtime":{"launcherAddress":"127.0.0.1:9876","database":"my/accounts.db"},"data":{"images":"my/images"},"android":{"serial":"selected-emulator","package":"my.package","redirectHosts":["old.example"]},"client":{"appVersion":"1.7.2"},"contracts":{"old":true},"patch":{"old":true}}`,
		published: `{"runtime":{"launcherAddress":"127.0.0.1:8080"},"data":{},"android":{"serial":"","package":"default.package","redirectHosts":["new.example"]},"client":{"appVersion":"1.7.5"},"contracts":{"new":true},"patch":{"new":true}}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := releaseConfig(local, published)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"runtime":   map[string]any{"launcherAddress": "127.0.0.1:9876", "database": "my/accounts.db"},
		"data":      map[string]any{"images": "my/images"},
		"android":   map[string]any{"serial": "selected-emulator", "package": "my.package", "redirectHosts": []any{"new.example"}},
		"client":    map[string]any{"appVersion": "1.7.5"},
		"contracts": map[string]any{"new": true},
		"patch":     map[string]any{"new": true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged config=%s, want %#v", data, want)
	}
}

func TestReplacementRestoresPartialInstall(t *testing.T) {
	root := t.TempDir()
	updates := filepath.Join(root, "data", "updates")
	if err := os.MkdirAll(updates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := newReplacement(root, "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"VERSION", "profiles/new.json"} {
		if err = files.save(path); err != nil {
			t.Fatal(err)
		}
	}
	newFile := filepath.Join(updates, "payload")
	if err = os.WriteFile(newFile, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"VERSION", "profiles/new.json"} {
		if err = replaceFile(newFile, filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatal(err)
		}
	}
	if err = files.restore(); err != nil {
		t.Fatal(err)
	}
	version, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil || string(version) != "original" {
		t.Fatalf("restored version=%q: %v", version, err)
	}
	if _, err = os.Stat(filepath.Join(root, "profiles", "new.json")); !os.IsNotExist(err) {
		t.Fatalf("new file survived rollback: %v", err)
	}
}
