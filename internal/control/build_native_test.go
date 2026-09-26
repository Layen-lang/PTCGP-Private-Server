package control

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeBuildDetectsChangedInputsAndMissingReader(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-time.Hour)
	write := func(name string, when time.Time) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want bool) {
		t.Helper()
		got, err := nativeNeedsBuild(root)
		if err != nil || got != want {
			t.Fatalf("needs build = %v, want %v: %v", got, want, err)
		}
	}
	write("native/importer/src/main.rs", old)
	check(true)
	for _, name := range nativeExecutables {
		write(filepath.Join("bin", name), old.Add(time.Minute))
	}
	check(false)
	// Cargo output must not make an otherwise unchanged source tree dirty.
	write("native/importer/target/generated", time.Now())
	check(false)
	for _, input := range []string{"native/importer/src/main.rs", "native/reader/src/main.rs", "native/importer/Cargo.lock", "native/reader/Cargo.toml", "native/build.ps1"} {
		write(input, old.Add(2*time.Minute))
		check(true)
		write(input, old)
		check(false)
	}
	if err := os.Remove(filepath.Join(root, "bin", "ptcgp-reader-aarch64")); err != nil {
		t.Fatal(err)
	}
	check(true)
}
