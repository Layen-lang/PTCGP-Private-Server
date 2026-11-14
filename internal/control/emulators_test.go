package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

type connectedEmulators struct{}

func (connectedEmulators) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if len(args) == 3 && args[0] == "-s" && args[2] == "get-state" && args[1] != "missing" {
		return []byte("device"), nil
	}
	return nil, fmt.Errorf("device unavailable")
}

type emulatorFake struct {
	cfg         configuration.Config
	status      Status
	actions     []Action
	launches    int
	statusCalls int
	err         error
}

func (d *emulatorFake) Status(context.Context) (Status, error)     { d.statusCalls++; return d.status, nil }
func (d *emulatorFake) SetJournal(*Journal)                        {}
func (d *emulatorFake) SetPreparedConfig(cfg configuration.Config) { d.cfg = cfg }
func (d *emulatorFake) running() bool                              { return d.status.Server.Running }
func (d *emulatorFake) launch(context.Context) error {
	d.launches++
	d.status.Android.Running = d.err == nil
	return d.err
}
func (d *emulatorFake) Action(_ context.Context, action Action) (Status, error) {
	d.actions = append(d.actions, action)
	d.status.Mode = string(action)
	d.status.Server.Running = action == ActionLocal
	if action == ActionStop {
		d.status.Mode = "stopped"
	}
	if action != ActionLocal {
		d.status.Android.Running = false
	}
	return d.status, d.err
}

func multiRunnerFixture(t *testing.T) (*MultiRunner, []store.Player) {
	t.Helper()
	root := t.TempDir()
	state, err := store.Open(context.Background(), filepath.Join(root, "players.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	cfg := configuration.Config{Path: filepath.Join(root, "server.json"), Runtime: configuration.Runtime{RuntimeDirectory: filepath.Join(root, "runtime"), Address: "127.0.0.1:14443", AdminAddress: "127.0.0.1:18081"}}
	m, err := NewMultiRunner(cfg, root, state)
	if err != nil {
		t.Fatal(err)
	}
	m.base.commands = connectedEmulators{}
	m.newDevice = func(cfg configuration.Config) (emulatorControl, error) {
		return &emulatorFake{cfg: cfg, status: Status{Mode: "online", Android: AndroidStatus{Serial: cfg.Android.Serial, Connected: true}}}, nil
	}
	var players []store.Player
	for _, name := range []string{"A", "B", "C"} {
		p, err := state.CreatePlayerWithInventory(context.Background(), name, 1, 0, false, store.InitialInventory{})
		if err != nil {
			t.Fatal(err)
		}
		players = append(players, p)
	}
	return m, players
}

func TestParallelEmulatorsSelectLaunchSwitchModeAndStopAll(t *testing.T) {
	ctx := context.Background()
	m, profiles := multiRunnerFixture(t)
	for i, serial := range []string{"MuMu", "LD9"} {
		if _, err := m.SelectDevice(ctx, serial); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Action(ctx, ActionLocal); err != nil {
			t.Fatal(err)
		}
		if err := m.OpenPlayer(player.WithEmulator(ctx, serial), profiles[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	a, b := m.devices["MuMu"].(*emulatorFake), m.devices["LD9"].(*emulatorFake)
	if !a.running() || !b.running() || a.launches != 1 || b.launches != 1 {
		t.Fatal("the two games did not remain active together")
	}
	if a.cfg.Runtime.Address == b.cfg.Runtime.Address || a.cfg.Runtime.AdminAddress == b.cfg.Runtime.AdminAddress || a.cfg.Runtime.RuntimeDirectory == b.cfg.Runtime.RuntimeDirectory {
		t.Fatal("devices shared process or tunnel runtime state")
	}
	if err := m.OpenPlayer(ctx, profiles[0].ID); !errors.Is(err, store.ErrPlayerInUse) {
		t.Fatalf("duplicate launch error = %v", err)
	}
	if b.launches != 1 {
		t.Fatal("a conflicting account restarted LD9")
	}
	if _, err := m.Action(ctx, ActionOnline); err != nil {
		t.Fatal(err)
	}
	if !a.running() || b.running() {
		t.Fatal("Official on LD9 stopped MuMu")
	}
	if _, err := m.SelectDevice(ctx, "MuMu"); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenPlayer(ctx, profiles[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(ctx, ActionStop); err != nil {
		t.Fatal(err)
	}
	if a.running() || b.running() {
		t.Fatal("Stop all left a server running")
	}
	if a.actions[len(a.actions)-1] != ActionStop || b.actions[len(b.actions)-1] != ActionStop {
		t.Fatal("Stop all did not visit both emulators")
	}
	if owners, err := m.state.EmulatorPlayers(ctx); err != nil || len(owners) != 0 {
		t.Fatalf("remaining account reservations = %v, err = %v", owners, err)
	}
}

func TestFailedLaunchRestoresAccountAndDoesNotAffectOtherEmulator(t *testing.T) {
	ctx := context.Background()
	m, profiles := multiRunnerFixture(t)
	for i, serial := range []string{"MuMu", "LD9"} {
		m.SelectDevice(ctx, serial)
		m.Action(ctx, ActionLocal)
		if err := m.OpenPlayer(ctx, profiles[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	m.devices["LD9"].(*emulatorFake).err = fmt.Errorf("game failed to start")
	if err := m.OpenPlayer(ctx, profiles[2].ID); err == nil {
		t.Fatal("failed launch was accepted")
	}
	owners, err := m.state.EmulatorPlayers(ctx)
	if err != nil || owners[profiles[0].ID] != "MuMu" || owners[profiles[1].ID] != "LD9" || owners[profiles[2].ID] != "" {
		t.Fatalf("reservations after launch failure = %v, err = %v", owners, err)
	}
}

func TestDeviceSelectionEndpointIsProtectedAndSkipsPreparation(t *testing.T) {
	m, _ := multiRunnerFixture(t)
	backend, _ := url.Parse("http://127.0.0.1:18081")
	h, err := New(backend, m)
	if err != nil {
		t.Fatal(err)
	}
	request := func(serial string, authenticated bool) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/control/device", strings.NewReader(fmt.Sprintf(`{"serial":%q}`, serial)))
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "ptcgp_control_csrf", Value: h.csrf})
			r.Header.Set("X-Control-CSRF-Token", h.csrf)
		}
		return r
	}
	for _, check := range []struct {
		serial        string
		authenticated bool
		status        int
	}{{"MuMu", false, 403}, {"MuMu", true, 200}, {"missing", true, 400}} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request(check.serial, check.authenticated))
		if response.Code != check.status {
			t.Fatalf("device %s status = %d, body = %s", check.serial, response.Code, response.Body.String())
		}
	}
	if m.selected != "MuMu" || m.devices["MuMu"].(*emulatorFake).launches != 0 || len(m.devices["MuMu"].(*emulatorFake).actions) != 0 {
		t.Fatal("selection changed the running games")
	}
	if !h.tryStartOperation("local") {
		t.Fatal("operation lock failed")
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request("LD9", true))
	if response.Code != http.StatusConflict || m.selected != "MuMu" {
		t.Fatal("selection raced a mode operation")
	}
	h.finishOperation()
}

func TestClosedGameReleasesItsAccountWithoutStoppingTheOtherGame(t *testing.T) {
	ctx := context.Background()
	m, profiles := multiRunnerFixture(t)
	for i, serial := range []string{"MuMu", "LD9"} {
		if _, err := m.SelectDevice(ctx, serial); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Action(ctx, ActionLocal); err != nil {
			t.Fatal(err)
		}
		if err := m.OpenPlayer(ctx, profiles[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	m.devices["MuMu"].(*emulatorFake).status.Android.Running = false
	// A startup delay must not release an account immediately.
	if _, err := m.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if owners, err := m.state.EmulatorPlayers(ctx); err != nil || owners[profiles[0].ID] != "MuMu" {
		t.Fatal("startup reservation was lost", owners, err)
	}
	m.now = func() time.Time { return time.Now().Add(time.Minute) }
	if _, err := m.Status(ctx); err != nil {
		t.Fatal(err)
	}
	owners, err := m.state.EmulatorPlayers(ctx)
	if err != nil || owners[profiles[0].ID] != "" || owners[profiles[1].ID] != "LD9" {
		t.Fatalf("reservations after closing MuMu = %v, err = %v", owners, err)
	}
	if !m.devices["LD9"].(*emulatorFake).status.Android.Running {
		t.Fatal("closing MuMu interrupted LD9")
	}
	if err := m.OpenPlayer(ctx, profiles[0].ID); err != nil {
		t.Fatal("released account could not be opened on LD9:", err)
	}
}

func TestSavedEmulatorRuntimesRestoreIndependentPorts(t *testing.T) {
	m, _ := multiRunnerFixture(t)
	for _, serial := range []string{"MuMu", "LD9"} {
		if _, err := m.SelectDevice(context.Background(), serial); err != nil {
			t.Fatal(err)
		}
	}
	cfg := m.base.cfg
	cfg.Android.Serial = "LD9"
	restored, err := NewMultiRunner(cfg, m.base.root, m.state)
	if err != nil {
		t.Fatal(err)
	}
	serial, backend := restored.Routing()
	if serial != "LD9" || backend != m.runtimes["LD9"].AdminAddress || len(restored.devices) != 2 {
		t.Fatal("saved runtimes were not restored independently")
	}
	for serial, runtime := range m.runtimes {
		if restored.runtimes[serial] != runtime {
			t.Fatalf("runtime changed for %s", serial)
		}
	}
}

func TestPendingRestorationChecksTheUnselectedEmulator(t *testing.T) {
	m, _ := multiRunnerFixture(t)
	for _, serial := range []string{"MuMu", "LD9"} {
		if _, err := m.SelectDevice(context.Background(), serial); err != nil {
			t.Fatal(err)
		}
	}
	a := m.devices["MuMu"].(*emulatorFake)
	before := a.statusCalls
	root := m.deviceConfig(m.runtimes["MuMu"]).Runtime.RuntimeDirectory
	if err := os.WriteFile(filepath.Join(root, "pending-rollback"), []byte("stop requested"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "installed-patch.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	a.status.Android.RecoveryError = "restoration pending"
	status, err := m.Status(context.Background())
	if err != nil || a.statusCalls != before+1 || m.selected != "LD9" || !strings.Contains(status.Android.RecoveryError, "MuMu: restoration pending") {
		t.Fatalf("deferred restoration = %+v, calls = %d, err = %v", status, a.statusCalls, err)
	}
	// Later polls reuse the result until the retry delay has passed.
	status, err = m.Status(context.Background())
	if err != nil || a.statusCalls != before+1 || !strings.Contains(status.Android.RecoveryError, "MuMu: restoration pending") {
		t.Fatalf("second poll = %+v, calls = %d, err = %v", status, a.statusCalls, err)
	}
	m.now = func() time.Time { return time.Now().Add(time.Minute) }
	if _, err := m.Status(context.Background()); err != nil || a.statusCalls != before+2 {
		t.Fatalf("restoration was not retried after the delay: calls = %d, err = %v", a.statusCalls, err)
	}
}

func TestStaleRestorationMarkerOnAnUnselectedUntouchedEmulatorIsCleared(t *testing.T) {
	m, _ := multiRunnerFixture(t)
	for _, serial := range []string{"MuMu", "LD9"} {
		if _, err := m.SelectDevice(context.Background(), serial); err != nil {
			t.Fatal(err)
		}
	}
	a := m.devices["MuMu"].(*emulatorFake)
	before := a.statusCalls
	pending := filepath.Join(m.deviceConfig(m.runtimes["MuMu"]).Runtime.RuntimeDirectory, "pending-rollback")
	if err := os.WriteFile(pending, []byte("stop requested"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pending); !errors.Is(err, os.ErrNotExist) || a.statusCalls != before {
		t.Fatalf("stale marker = %v, calls = %d", err, a.statusCalls)
	}
}

type emulatorAliasRunner struct{ connectedEmulators }

func (r emulatorAliasRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) == 5 && args[2] == "shell" && args[3] == "cat" && args[4] == "/proc/sys/kernel/random/boot_id" {
		if args[1] == "LD9" {
			return []byte("ld9-boot"), nil
		}
		return []byte("mumu-boot"), nil
	}
	return r.connectedEmulators.Run(ctx, name, args...)
}

func TestSelectingAnADBAliasKeepsTheSameRuntimeAndAccount(t *testing.T) {
	m, profiles := multiRunnerFixture(t)
	m.base.commands = emulatorAliasRunner{}
	ctx := context.Background()
	if _, err := m.SelectDevice(ctx, "MuMu"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(ctx, ActionLocal); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenPlayer(ctx, profiles[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SelectDevice(ctx, "LD9"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SelectDevice(ctx, "emulator-5554"); err != nil {
		t.Fatal(err)
	}
	if m.selected != "MuMu" || len(m.runtimes) != 2 || m.devices["MuMu"].(*emulatorFake).launches != 1 {
		t.Fatal("an ADB alias created or restarted a second runtime")
	}
}

// concurrentStop records whether two devices' stops overlapped.
type concurrentStop struct {
	*emulatorFake
	active, overlap *int32
}

func (d concurrentStop) Action(ctx context.Context, action Action) (Status, error) {
	if atomic.AddInt32(d.active, 1) > 1 {
		atomic.StoreInt32(d.overlap, 1)
	}
	time.Sleep(30 * time.Millisecond)
	atomic.AddInt32(d.active, -1)
	return d.emulatorFake.Action(ctx, action)
}

func TestStopAllRestoresSerialsOfOneAndroidOneAfterTheOther(t *testing.T) {
	m, _ := multiRunnerFixture(t)
	// "BlueStacks" is a stopped emulator's old serial that now reaches LD9.
	m.base.commands = sharedAndroidRunner{}
	var active, overlap int32
	for _, serial := range []string{"BlueStacks", "LD9"} {
		m.devices[serial] = concurrentStop{&emulatorFake{status: Status{Server: ServerStatus{Running: true}}}, &active, &overlap}
	}
	m.selected = "LD9"
	m.runtimes["LD9"] = emulatorRuntime{Serial: "LD9"}
	m.runtimes["BlueStacks"] = emulatorRuntime{Serial: "BlueStacks"}
	groups := m.androidGroups(context.Background())
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("groups = %v", groups)
	}
	if _, err := m.stopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if overlap != 0 {
		t.Fatal("one Android was restored twice at the same time")
	}
	for _, serial := range []string{"BlueStacks", "LD9"} {
		if fake := m.devices[serial].(concurrentStop); len(fake.actions) != 1 || fake.actions[0] != ActionStop {
			t.Fatalf("%s actions = %v", serial, fake.actions)
		}
	}
}

type sharedAndroidRunner struct{ connectedEmulators }

func (r sharedAndroidRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) == 5 && args[4] == "/proc/sys/kernel/random/boot_id" {
		return []byte("ld9-boot\n"), nil
	}
	return r.connectedEmulators.Run(ctx, name, args...)
}

type noEmulatorRunner struct{ listings *int }

func (r noEmulatorRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if strings.Join(args, " ") == "devices -l" {
		*r.listings++
		return []byte("List of devices attached\n"), nil
	}
	return nil, fmt.Errorf("device unavailable")
}

func TestStatusWithoutEmulatorDiscoversOnce(t *testing.T) {
	m, _ := multiRunnerFixture(t)
	listings := 0
	m.base.commands = noEmulatorRunner{listings: &listings}
	m.selected = ""
	status, err := m.Status(context.Background())
	if err != nil || status.Android.Connected || status.Mode != "unknown" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if listings != 1 {
		t.Fatalf("ADB device listings = %d, want 1", listings)
	}
}

func TestForgetDeviceRemovesOnlyUntouchedUnselectedDevices(t *testing.T) {
	ctx := context.Background()
	m, profiles := multiRunnerFixture(t)
	for _, serial := range []string{"MuMu", "LD9", "Phone"} {
		if _, err := m.SelectDevice(ctx, serial); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ForgetDevice(ctx, "Phone"); err == nil {
		t.Fatal("forgot the selected device")
	}
	ld9 := m.deviceConfig(m.runtimes["LD9"]).Runtime.RuntimeDirectory
	if err := os.MkdirAll(ld9, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ld9, "installed-patch.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ForgetDevice(ctx, "LD9"); err == nil {
		t.Fatal("forgot a device that still needs restoration")
	}
	if err := m.state.SelectEmulatorPlayer(ctx, "MuMu", profiles[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.ForgetDevice(ctx, "MuMu"); err != nil {
		t.Fatal(err)
	}
	if _, exists := m.runtimes["MuMu"]; exists || m.devices["MuMu"] != nil {
		t.Fatal("device still managed")
	}
	if owners, err := m.state.EmulatorPlayers(ctx); err != nil || owners[profiles[0].ID] != "" {
		t.Fatalf("reservation kept: %v %v", owners, err)
	}
	restored, err := NewMultiRunner(m.base.cfg, m.base.root, m.state)
	if err != nil || len(restored.runtimes) != 2 {
		t.Fatalf("saved runtimes = %d, err = %v", len(restored.runtimes), err)
	}
	listed := m.ManagedDevices()
	if len(listed) != 2 || !listed[0].NeedsRestoration || listed[0].Serial != "LD9" || !listed[1].Selected {
		t.Fatalf("managed devices = %+v", listed)
	}
}

func TestOpeningAnAccountEnablesLocalModeFirst(t *testing.T) {
	m, profiles := multiRunnerFixture(t)
	if _, err := m.SelectDevice(context.Background(), "MuMu"); err != nil {
		t.Fatal(err)
	}
	backend, _ := url.Parse("http://127.0.0.1:18081")
	h, err := New(backend, m)
	if err != nil {
		t.Fatal(err)
	}
	ctx := player.WithEmulator(context.Background(), "MuMu")
	if err := h.OpenPlayer(ctx, profiles[0].ID); err != nil {
		t.Fatal(err)
	}
	device := m.devices["MuMu"].(*emulatorFake)
	if len(device.actions) != 1 || device.actions[0] != ActionLocal || device.launches != 1 {
		t.Fatalf("actions = %v, launches = %d", device.actions, device.launches)
	}
	// Already local: the next launch does not switch again.
	if err := h.OpenPlayer(ctx, profiles[1].ID); err != nil {
		t.Fatal(err)
	}
	if len(device.actions) != 1 || device.launches != 2 {
		t.Fatalf("actions = %v, launches = %d", device.actions, device.launches)
	}
	if err := h.OpenPlayer(player.WithEmulator(context.Background(), "LD9"), profiles[2].ID); err == nil || device.launches != 2 {
		t.Fatal("launched on a device other than the one the user saw")
	}
}
