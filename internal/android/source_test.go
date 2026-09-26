package android

import (
	"context"
	"fmt"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"strings"
	"testing"
)

type sourceRunner struct {
	abi, version, hash string
	root               bool
	calls              []string
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
	case strings.Contains(s, "sha256sum"):
		return []byte(r.hash + " file"), nil
	case strings.Contains(s, "od -An"):
		return []byte(r.abi), nil
	case strings.Contains(s, "EXTERNAL_STORAGE"):
		return []byte("/storage/emulated/10"), nil
	case strings.Contains(s, "test -"):
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected command %s", s)
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
