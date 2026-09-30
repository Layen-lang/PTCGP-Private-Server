package android

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

type sourceRunner struct {
	abi, version, hash string
	root               bool
	calls              []string
	missingLibrary     bool
	missingAPKLibrary  bool
	unreadableLibrary  bool
}

func (r *sourceRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	s := strings.Join(args, " ")
	r.calls = append(r.calls, s)
	switch {
	case strings.Contains(s, "get-state"):
		return []byte("device"), nil
	case strings.Contains(s, "id -u"):
		if r.root {
			return []byte("0"), nil
		}
		return []byte("2000"), nil
	case strings.Contains(s, "dumpsys"):
		return []byte("versionName=" + r.version + "\nnativeLibraryDir=/data/app/game/lib/arm64"), nil
	case strings.Contains(s, "pm path"):
		return []byte("package:/data/app/game/base.apk\npackage:/data/app/game/another-assets.apk"), nil
	case strings.Contains(s, "unzip -l"):
		if !r.missingAPKLibrary && strings.Contains(s, "another-assets.apk") {
			return nil, nil
		}
		return nil, fmt.Errorf("entry missing")
	case strings.Contains(s, "unzip -p"):
		return []byte(r.hash + " -"), nil
	case strings.Contains(s, "sha256sum"):
		if r.missingLibrary || r.unreadableLibrary {
			return []byte("cannot read library"), fmt.Errorf("exit status 1")
		}
		return []byte(r.hash + " file"), nil
	case strings.Contains(s, "test -f"):
		if r.missingLibrary {
			return nil, fmt.Errorf("file missing")
		}
		return nil, nil
	case strings.Contains(s, "od -An"):
		return []byte(r.abi), nil
	case strings.Contains(s, "EXTERNAL_STORAGE"):
		return []byte("/storage/emulated/10"), nil
	case strings.Contains(s, "test -"):
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected command %s", s)
}

func TestInspectLibraryInAPK(t *testing.T) {
	for _, test := range []struct {
		name, hash, wantError string
		missingAPKLibrary     bool
	}{
		{name: "validated split library", hash: strings.Repeat("a", 64)},
		{name: "unknown split library", hash: strings.Repeat("c", 64), wantError: "validated compatibility profile"},
		{name: "missing split library", missingAPKLibrary: true, wantError: "in installed APKs"},
		{name: "empty extraction", hash: "", wantError: "validated compatibility profile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := &sourceRunner{abi: "62 0", version: "1.7.2", hash: test.hash, root: true, missingLibrary: true, missingAPKLibrary: test.missingAPKLibrary}
			_, err := Inspect(context.Background(), r, sourceConfig())
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			for _, call := range r.calls {
				if strings.Contains(call, "push") || strings.Contains(call, "mkdir") || strings.Contains(call, "chmod") || strings.Contains(call, " > ") {
					t.Fatalf("inspection mutated device: %s", call)
				}
			}
		})
	}
}

func TestInspectDoesNotBypassUnreadableExtractedLibrary(t *testing.T) {
	r := &sourceRunner{version: "1.7.2", root: true, unreadableLibrary: true}
	_, err := Inspect(context.Background(), r, sourceConfig())
	if err == nil || !strings.Contains(err.Error(), "cannot read library") {
		t.Fatalf("error = %v, want library read diagnostic", err)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "unzip") {
			t.Fatalf("bypassed extracted library: %s", call)
		}
	}
}
func sourceConfig() configuration.Config {
	var c configuration.Config
	c.Android.Serial = "emulator"
	c.Android.Package = "game"
	c.Client.AppVersion = "1.7.2"
	c.Client.MasterMemoryAladdinHash = "aa"
	c.Client.AndroidAssetAladdinHash = "bb"
	c.Patch.Library = "lib.so"
	c.Patch.SourceSHA256 = strings.Repeat("a", 64)
	return c
}
func TestEmulatorCapabilitiesUseActualSystemELF(t *testing.T) {
	for _, test := range []struct{ elf, abi string }{{"62 0", "x86_64"}, {"183 0", "aarch64"}} {
		r := &sourceRunner{abi: test.elf, version: "1.7.2", hash: strings.Repeat("a", 64), root: true}
		s, e := Inspect(context.Background(), r, sourceConfig())
		if e != nil {
			t.Fatal(e)
		}
		if s.ABI != test.abi || len(s.APKs) != 2 || !strings.Contains(s.Root, "/10/") {
			t.Fatalf("%+v", s)
		}
	}
}
func TestUnsupportedGameFailsBeforeAnyMutation(t *testing.T) {
	r := &sourceRunner{version: "2.0.0", root: true}
	if _, e := Inspect(context.Background(), r, sourceConfig()); e == nil {
		t.Fatal("accepted incompatible game")
	}
	for _, call := range r.calls {
		if strings.Contains(call, "push") || strings.Contains(call, "force-stop") || strings.Contains(call, "sha256sum") {
			t.Fatalf("unexpected operation after version mismatch: %s", call)
		}
	}
}
