package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/binarypatch"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

type androidStateRunner struct {
	connected      bool
	calls          []string
	data           []byte
	backupData     []byte
	backupHash     string
	missingBackup  bool
	missingLibrary bool
}

func (r *androidStateRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	switch {
	case strings.HasPrefix(call, "connect "):
		return nil, nil
	case call == "-s serial get-state":
		if r.connected {
			return []byte("device"), nil
		}
		return nil, fmt.Errorf("device offline")
	case call == "devices -l":
		if r.connected {
			return []byte("List of devices attached\nserial device\n"), nil
		}
		return []byte("List of devices attached\n"), nil
	case strings.Contains(call, "shell pm path"):
		return []byte("package:/data/app/game/base.apk\npackage:/data/app/game/split_config.arm64_v8a.apk"), nil
	case strings.Contains(call, "shell dumpsys package"):
		return []byte("versionName=1.0.0"), nil
	case strings.Contains(call, "id -u"):
		return []byte("0"), nil
	case strings.Contains(call, "shell cat /system/etc/hosts"):
		return []byte("127.0.0.1 localhost"), nil
	case strings.Contains(call, "shell pidof"):
		return nil, fmt.Errorf("not running")
	case len(args) > 2 && args[2] == "pull":
		return nil, os.WriteFile(args[len(args)-1], r.data, 0600)
	case len(args) > 2 && args[2] == "push":
		return nil, nil
	case strings.Contains(call, "sha256sum"):
		if strings.Contains(call, ".original") {
			return []byte(r.backupHash + " file"), nil
		}
		return []byte(fmt.Sprintf("%x file", sha256.Sum256(r.data))), nil
	case strings.Contains(call, "cat ") && strings.Contains(call, ".original"):
		r.data = append(r.data[:0], r.backupData...)
		return nil, nil
	case strings.Contains(call, "am force-stop"), strings.Contains(call, "MissionUserPrefs"), strings.Contains(call, "umount"):
		return nil, nil
	case strings.Contains(call, "test -f") && strings.Contains(call, "/data/app/game/lib/arm64/lib.so"):
		if r.missingLibrary {
			return nil, fmt.Errorf("missing library")
		}
		return nil, nil
	case strings.Contains(call, "unzip -l"):
		if strings.Contains(call, "split_config.arm64_v8a.apk") {
			return nil, nil
		}
		return nil, fmt.Errorf("entry missing")
	case strings.Contains(call, "unzip -p"):
		return nil, nil
	case strings.Contains(call, "ptcgp-push-") && strings.Contains(call, "cp "):
		return nil, nil
	case call == "-s serial reverse --list":
		return nil, nil
	case strings.Contains(call, "test -e"):
		if r.missingBackup {
			return nil, fmt.Errorf("missing backup")
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected command: %s", call)
}

func TestEnsureLibraryPathMaterializesLibraryFromSplitAPK(t *testing.T) {
	commands := &androidStateRunner{connected: true, missingLibrary: true}
	r := testNativeRunner(t, commands)

	got, err := r.ensureLibraryPath(context.Background(), "serial", "lib.so")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/data/app/game/lib/arm64/lib.so" {
		t.Fatalf("path=%q", got)
	}
	calls := strings.Join(commands.calls, "\n")
	if !strings.Contains(calls, "split_config.arm64_v8a.apk") || !strings.Contains(calls, "unzip -p") || !strings.Contains(calls, "chmod 755") {
		t.Fatalf("library was not materialized safely:\n%s", calls)
	}
}

func TestPushRootFileUsesShellWritableStagingPath(t *testing.T) {
	commands := &androidStateRunner{connected: true}
	r := testNativeRunner(t, commands)

	if err := r.pushRootFile(context.Background(), "serial", "ca.pem", "/root-only/cert.0", "644"); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(commands.calls, "\n")
	if !strings.Contains(calls, "push ca.pem /data/local/tmp/ptcgp-push-cert.0") ||
		!strings.Contains(calls, "cp ") || !strings.Contains(calls, "/root-only/cert.0") {
		t.Fatalf("root file did not use staging:\n%s", calls)
	}
}

func TestTunnelRulesKeepLegacyOwnerStateAndSupportLoopbackFallback(t *testing.T) {
	legacy := tunnelRule(tunnelState{UID: 10123})
	if !strings.Contains(legacy, "--uid-owner 10123") {
		t.Fatalf("legacy rule lost owner scope: %s", legacy)
	}
	loopback := tunnelRule(tunnelState{UID: 10123, Rule: "loopback"})
	if strings.Contains(loopback, "--uid-owner") || !strings.Contains(loopback, "-d 127.0.0.1/32") {
		t.Fatalf("invalid loopback fallback: %s", loopback)
	}
}

func testNativeRunner(t *testing.T, commands *androidStateRunner) *NativeRunner {
	t.Helper()
	t.Setenv("ProgramData", t.TempDir())
	root := t.TempDir()
	cfg := configuration.Config{
		Path:    filepath.Join(root, "server.json"),
		Runtime: configuration.Runtime{RuntimeDirectory: filepath.Join(root, "runtime")},
		Android: configuration.Android{Package: "game"},
		Patch: binarypatch.Manifest{
			Name: "fixture", Version: "1.0.0", Library: "lib.so",
			SourceSHA256: fmt.Sprintf("%x", sha256.Sum256(commands.data)), TargetSHA256: strings.Repeat("f", 64),
			ExpectedHex: "00", ReplacementHex: "01",
		},
	}
	r, err := NewNativeRunner(cfg, root)
	if err != nil {
		t.Fatal(err)
	}
	r.commands = commands
	return r
}

func TestStopWithoutDeviceClosesServicesAndDefersRollback(t *testing.T) {
	r := testNativeRunner(t, &androidStateRunner{})
	status, err := r.Action(context.Background(), ActionStop)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "stopped" || status.Server.Running || status.Android.Connected {
		t.Fatalf("status=%+v", status)
	}
	if _, err := os.Stat(r.pendingRollback()); err != nil {
		t.Fatalf("pending rollback marker: %v", err)
	}
}

func TestStatusCompletesRollbackDeferredWhileDeviceWasOffline(t *testing.T) {
	original := []byte("official library")
	patched := []byte("patched library")
	commands := &androidStateRunner{
		connected:  true,
		data:       append([]byte(nil), patched...),
		backupData: original,
		backupHash: fmt.Sprintf("%x", sha256.Sum256(original)),
	}
	r := testNativeRunner(t, commands)
	manifest := binarypatch.Manifest{
		Name:           "fixture",
		Version:        "1.0.0",
		Library:        "lib.so",
		SourceSHA256:   fmt.Sprintf("%x", sha256.Sum256(original)),
		TargetSHA256:   fmt.Sprintf("%x", sha256.Sum256(patched)),
		ExpectedHex:    "00",
		ReplacementHex: "01",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	r.cfg.Patch = manifest
	if err := os.MkdirAll(r.cfg.Runtime.RuntimeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.installedPatch(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.markPendingRollback(); err != nil {
		t.Fatal(err)
	}

	status, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Android.Native != "officiel" || string(commands.data) != string(original) {
		t.Fatalf("status=%+v data=%q", status, commands.data)
	}
	if _, err := os.Stat(r.pendingRollback()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending rollback marker still present: %v", err)
	}
}

func TestStatusUsesLightweightChecksAfterInitialInspection(t *testing.T) {
	commands := &androidStateRunner{connected: true, data: []byte("official library")}
	r := testNativeRunner(t, commands)

	if _, err := r.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands.calls = nil

	if _, err := r.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"-s serial get-state", "-s serial shell pidof game"}
	if strings.Join(commands.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routine status performed expensive checks:\n%s", strings.Join(commands.calls, "\n"))
	}
}

func TestStatusRediscoversOnlyWhenRememberedDeviceStopsResponding(t *testing.T) {
	commands := &androidStateRunner{connected: true, data: []byte("official library")}
	r := testNativeRunner(t, commands)

	if _, err := r.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands.calls = nil
	commands.connected = false

	status, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Android.Connected {
		t.Fatalf("status=%+v", status)
	}
	calls := strings.Join(commands.calls, "\n")
	if !strings.HasPrefix(calls, "-s serial get-state\n") || !strings.Contains(calls, "devices -l") {
		t.Fatalf("missing fallback discovery:\n%s", calls)
	}
}

func TestOnlineRestoresOfficialModeWithoutStartingGame(t *testing.T) {
	official := []byte("official library")
	commands := &androidStateRunner{connected: true, data: official}
	r := testNativeRunner(t, commands)
	manifest := binarypatch.Manifest{
		Name:           "fixture",
		Version:        "1.0.0",
		Library:        "lib.so",
		SourceSHA256:   fmt.Sprintf("%x", sha256.Sum256(official)),
		TargetSHA256:   fmt.Sprintf("%x", sha256.Sum256([]byte("patched library"))),
		ExpectedHex:    "00",
		ReplacementHex: "01",
	}
	r.cfg.Patch = manifest

	status, err := r.Action(context.Background(), ActionOnline)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "online" || status.Android.Running {
		t.Fatalf("status=%+v", status)
	}
	for _, call := range commands.calls {
		if strings.Contains(call, "shell am start") {
			t.Fatalf("official mode started the game: %s", call)
		}
	}
}

func TestHTTPStatusDetectsLateDeviceAndDisappearance(t *testing.T) {
	commands := &androidStateRunner{}
	r := testNativeRunner(t, commands)
	backend, _ := url.Parse("http://127.0.0.1:1")
	h := newHandler(t, r, backend)
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, connected := range []bool{false, true, false, true} {
		commands.connected = connected
		response, err := srv.Client().Get(srv.URL + "/api/control/status")
		if err != nil {
			t.Fatal(err)
		}
		var status Status
		err = json.NewDecoder(response.Body).Decode(&status)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || status.Android.Connected != connected {
			t.Fatalf("connected=%t: status=%+v HTTP=%d", connected, status, response.StatusCode)
		}
	}
}

func TestStopWithoutEmulatorStopsAndDefersAndroidRollback(t *testing.T) {
	commands := &androidStateRunner{}
	r := testNativeRunner(t, commands)

	status, err := r.Action(context.Background(), ActionStop)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "stopped" || status.Server.Running || status.Android.Connected {
		t.Fatalf("status=%+v", status)
	}
	if _, err := os.Stat(r.pendingRollback()); err != nil {
		t.Fatalf("pending rollback marker: %v", err)
	}
}

func TestRollbackRejectsCorruptBackupBeforeWriting(t *testing.T) {
	commands := &androidStateRunner{connected: true, data: []byte("patched"), backupHash: strings.Repeat("0", 64)}
	r := testNativeRunner(t, commands)
	m := binarypatch.Manifest{Name: "fixture", Version: "1.0.0", Library: "lib.so", SourceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("original"))), TargetSHA256: fmt.Sprintf("%x", sha256.Sum256(commands.data)), ExpectedHex: "00", ReplacementHex: "01"}
	r.cfg.Patch = m
	if err := r.SetupAndroid(context.Background(), "rollback"); err == nil || !strings.Contains(err.Error(), "unexpected SHA-256") {
		t.Fatalf("error=%v", err)
	}
	for _, call := range commands.calls {
		if strings.Contains(call, " > ") || strings.Contains(call, "umount") {
			t.Fatalf("changed Android despite corrupt backup: %s", call)
		}
	}
}

func TestLocalModeRejectsPatchedLibraryWithoutUserBackup(t *testing.T) {
	patched := []byte("patched library")
	commands := &androidStateRunner{connected: true, data: patched, missingBackup: true}
	r := testNativeRunner(t, commands)
	m := binarypatch.Manifest{
		Name:           "fixture",
		Version:        "1.0.0",
		Library:        "lib.so",
		SourceSHA256:   fmt.Sprintf("%x", sha256.Sum256([]byte("official library"))),
		TargetSHA256:   fmt.Sprintf("%x", sha256.Sum256(patched)),
		ExpectedHex:    "00",
		ReplacementHex: "01",
	}
	r.cfg.Patch = m

	err := r.SetupAndroid(context.Background(), "apply")
	if err == nil || !strings.Contains(err.Error(), "reinstall or update the game") {
		t.Fatalf("error=%v", err)
	}
	for _, call := range commands.calls {
		if strings.Contains(call, " push ") || strings.Contains(call, " > ") {
			t.Fatalf("changed Android without an original backup: %s", call)
		}
	}
}

func TestHostsReplacementIsIdempotentAndPreservesOtherEntries(t *testing.T) {
	before := "127.0.0.1 localhost\r\n10.0.0.1 custom\r\n# PTCGP-LOCAL-BEGIN\r\n127.0.0.1 old\r\n# PTCGP-LOCAL-END\r\n"
	after := localHostsText(before, []string{"new.example"})
	if strings.Contains(after, " old") || !strings.Contains(after, "10.0.0.1 custom") || strings.Count(after, "# PTCGP-LOCAL-BEGIN") != 1 {
		t.Fatalf("unexpected hosts: %s", after)
	}
	if again := localHostsText(after, []string{"new.example"}); again != after {
		t.Fatalf("second apply changed hosts: %q != %q", again, after)
	}
}

func TestManagedPIDRejectsAnotherExecutable(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "server.pid")
	if err := os.WriteFile(filename, []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	if got := managedPID(filename, filepath.Join(t.TempDir(), "unrelated.exe")); got != 0 {
		t.Fatalf("unrelated process accepted: %d", got)
	}
}

func TestManagedPIDDoesNotTreatControllerAsPersistentLauncher(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := managedPID(filepath.Join(t.TempDir(), "missing.pid"), executable); got != 0 {
		t.Fatalf("current controller process accepted as persistent launcher: %d", got)
	}
}

func TestEnsureExecutablesUsesPublishedBinariesWithoutSourceTree(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ptcgp-server.exe", "ptcgp-launcher.exe"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("published"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner, err := NewNativeRunner(configuration.Config{}, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.EnsureExecutables(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWebDependenciesNeedInstall(t *testing.T) {
	webDir := t.TempDir()
	lock := filepath.Join(webDir, "package-lock.json")
	if err := os.WriteFile(lock, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	install, err := webDependenciesNeedInstall(webDir)
	if err != nil || !install {
		t.Fatalf("clean checkout: install=%t error=%v", install, err)
	}

	binDir := filepath.Join(webDir, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../.package-lock.json", "tsc.cmd", "vite.cmd"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("installed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	install, err = webDependenciesNeedInstall(webDir)
	if err != nil || install {
		t.Fatalf("installed checkout: install=%t error=%v", install, err)
	}

	newer := time.Now().Add(time.Second)
	if err := os.Chtimes(lock, newer, newer); err != nil {
		t.Fatal(err)
	}
	install, err = webDependenciesNeedInstall(webDir)
	if err != nil || !install {
		t.Fatalf("updated lockfile: install=%t error=%v", install, err)
	}
}
