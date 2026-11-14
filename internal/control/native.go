package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/android"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/binarypatch"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/installation"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/localtls"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
)

// NativeRunner controls the server and Android directly, without a shell host.
// The handler serializes operations and status reads.
type NativeRunner struct {
	cfg                    configuration.Config
	root, server, launcher string
	journal                *Journal
	commands               android.Runner
	cachedSerial           string
	cachedStatus           Status
	fullStatusValid        bool
	rollbackRetrySerial    string
	rollbackRetryAt        time.Time
	rollbackRetryError     error
	isolated               bool
	cache                  *actionCache
}

type nativeCommands struct{}

func (nativeCommands) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// NewNativeRunner resolves the executables installed in the project.
func NewNativeRunner(cfg configuration.Config, root string) (*NativeRunner, error) {
	program, err := installation.Program(root)
	if err != nil {
		return nil, err
	}
	r := &NativeRunner{cfg: cfg, root: root, commands: nativeCommands{},
		server: filepath.Join(program, "bin", "ptcgp-server.exe"), launcher: filepath.Join(program, "bin", "ptcgp-server.exe")}
	return r, nil
}

// SetJournal enables operation progress reporting before serving requests.
func (r *NativeRunner) SetJournal(journal *Journal) { r.journal = journal }

func (r *NativeRunner) step(action, message string) {
	if r.journal != nil {
		r.journal.Record(action, "info", message)
	}
}

func (r *NativeRunner) adb(ctx context.Context, serial string, args ...string) (string, error) {
	out, err := r.commands.Run(ctx, "adb", append([]string{"-s", serial}, args...)...)
	return strings.TrimSpace(string(out)), err
}

func (r *NativeRunner) shell(ctx context.Context, serial, command string) (string, error) {
	// ADB joins shell arguments before sending them to Android. Quote the
	// complete su argument so redirections and compound statements run as root.
	quoted := shellQuote(command)
	return r.adb(ctx, serial, "shell", "if [ \"$(id -u)\" = 0 ]; then sh -c "+quoted+"; else su -c "+quoted+"; fi")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (r *NativeRunner) discover(ctx context.Context) (string, error) {
	return android.Discover(ctx, r.commands, r.cfg.Android.Serial, r.cfg.Android.Package)
}

// Status performs one complete inspection, then limits routine polling to the
// remembered device's connection state and game process. A failed connection
// invalidates the cache and triggers discovery again.
func (r *NativeRunner) Status(ctx context.Context) (Status, error) {
	if r.rollbackRetryError != nil && !time.Now().Before(r.rollbackRetryAt) {
		r.fullStatusValid = false
	}
	if !r.fullStatusValid || r.cachedSerial == "" {
		return r.fullStatus(ctx, r.cachedSerial)
	}

	serial := r.cachedSerial
	state, err := r.adb(ctx, serial, "get-state")
	if err != nil || state != "device" {
		r.cachedSerial = ""
		r.fullStatusValid = false
		return r.fullStatus(ctx, "")
	}

	s := r.cachedStatus
	s.Server.PID = r.processPID("server", r.server)
	s.Server.Running = s.Server.PID != 0
	pid, err := r.adb(ctx, serial, "shell", "pidof", r.cfg.Android.Package)
	s.Android.Running = err == nil && pid != ""
	s.Android.Game = "arrete"
	if s.Android.Running {
		s.Android.Game = "demarre (PID " + pid + ")"
	}
	r.cachedStatus = s
	if ctx.Err() != nil {
		return s, ctx.Err()
	}
	return s, nil
}

// disconnectedStatus describes the local server while no device is usable.
func (r *NativeRunner) disconnectedStatus(ctx context.Context) (Status, error) {
	s := Status{Mode: "unknown", Server: ServerStatus{PID: r.processPID("server", r.server)},
		Android: AndroidStatus{Routing: "indisponible", CA: "inconnue", Native: "inconnue", Game: "inconnu"}}
	s.Server.Running = s.Server.PID != 0
	r.cachedSerial = ""
	r.fullStatusValid = false
	return s, ctx.Err()
}

func (r *NativeRunner) fullStatus(ctx context.Context, serial string) (Status, error) {
	s := Status{Mode: "unknown", Server: ServerStatus{PID: r.processPID("server", r.server)},
		Android: AndroidStatus{Routing: "indisponible", CA: "inconnue", Native: "inconnue", Game: "inconnu"}}
	s.Server.Running = s.Server.PID != 0
	var err error
	if serial != "" {
		state, err := r.adb(ctx, serial, "get-state")
		if err != nil || state != "device" {
			serial = ""
		}
	}
	if serial == "" {
		serial, err = r.discover(ctx)
		if err != nil {
			return r.disconnectedStatus(ctx)
		}
	}
	// A starting operation may interrupt a status check, but never a
	// restoration halfway through writing the native library.
	recoveryCtx, cancelRecovery := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	err = r.recoverPendingRollback(recoveryCtx, serial)
	cancelRecovery()
	if err != nil {
		// Restoration remains pending, but status must stay available so the
		// user can resolve an emulator-specific failure.
		s.Android.RecoveryError = err.Error()
	}
	a := &s.Android
	a.Serial, a.Connected = serial, true
	uid, err := r.shell(ctx, serial, "id -u")
	a.Root = err == nil && uid == "0"
	hosts, err := r.adb(ctx, serial, "shell", "cat", "/system/etc/hosts")
	if err == nil {
		a.Routing = "officiel"
		if strings.Contains(hosts, "# PTCGP-LOCAL-BEGIN") {
			a.Routing = "local"
		}
	}
	if name, err := localtls.AndroidSystemName(r.cfg.Runtime.CertificateAuthority); err == nil && a.Root {
		if r.androidCAInstalled(ctx, serial, name) {
			a.CA = "locale installee"
		} else {
			a.CA = "officielle uniquement"
		}
	}
	manifest, err := r.manifest(false)
	if err == nil && a.Root {
		if library, err := r.libraryPath(ctx, serial, manifest.Library); err == nil {
			hash, err := r.remoteHash(ctx, serial, library)
			if err != nil {
				a.Native = "introuvable"
			} else {
				switch hash {
				case manifest.SourceSHA256:
					a.Native = "officiel"
				case manifest.TargetSHA256:
					a.Native = "patche local"
				default:
					a.Native = "inconnu (" + hash + ")"
				}
			}
		}
	}
	pid, err := r.adb(ctx, serial, "shell", "pidof", r.cfg.Android.Package)
	a.Running = err == nil && pid != ""
	a.Game = "arrete"
	if a.Running {
		a.Game = "demarre (PID " + pid + ")"
	}
	switch {
	case a.Routing == "local":
		s.Mode = "local"
	default:
		switch r.savedMode() {
		case "local":
			s.Mode = "local"
		case "online":
			s.Mode = "online"
		case "stopped":
			s.Mode = "stopped"
		case "":
			if a.Running {
				s.Mode = "online"
			} else {
				s.Mode = "stopped"
			}
		}
	}
	if ctx.Err() != nil {
		r.fullStatusValid = false
		return s, ctx.Err()
	}
	r.cachedSerial = serial
	r.cachedStatus = s
	r.fullStatusValid = true
	return s, nil
}

func (r *NativeRunner) manifest(rollback bool) (binarypatch.Manifest, error) {
	if rollback {
		if _, err := os.Stat(r.installedPatch()); err == nil {
			return binarypatch.LoadManifest(r.installedPatch())
		} else if !errors.Is(err, os.ErrNotExist) {
			return binarypatch.Manifest{}, err
		}
	}
	return r.cfg.Patch, r.cfg.Patch.Validate()
}

func (r *NativeRunner) installedPatch() string {
	return filepath.Join(r.cfg.Runtime.RuntimeDirectory, "installed-patch.json")
}

func (r *NativeRunner) pendingRollback() string {
	return filepath.Join(r.cfg.Runtime.RuntimeDirectory, "pending-rollback")
}

func (r *NativeRunner) modeFile() string {
	return filepath.Join(r.cfg.Runtime.RuntimeDirectory, "mode")
}

func (r *NativeRunner) setMode(mode string) error {
	if err := os.MkdirAll(r.cfg.Runtime.RuntimeDirectory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(r.modeFile(), []byte(mode+"\n"), 0o600)
}

func (r *NativeRunner) savedMode() string {
	data, err := os.ReadFile(r.modeFile())
	if err != nil {
		return ""
	}
	mode := strings.TrimSpace(string(data))
	if mode == "local" || mode == "online" || mode == "stopped" {
		return mode
	}
	return ""
}

// runtimeNeedsRestoration reports whether a device runtime directory records
// Android changes that Official or Stop must undo: a patched library (saved
// before the first native write), a fallback tunnel, or local routing.
func runtimeNeedsRestoration(directory string) bool {
	for _, name := range []string{"installed-patch.json", "adb-tunnel.json"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			return true
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "mode"))
	return err == nil && strings.TrimSpace(string(data)) == "local"
}

func (r *NativeRunner) needsRestoration() bool {
	return runtimeNeedsRestoration(r.cfg.Runtime.RuntimeDirectory)
}

func (r *NativeRunner) markPendingRollback() error {
	if err := os.MkdirAll(r.cfg.Runtime.RuntimeDirectory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(r.pendingRollback(), []byte("stop requested\n"), 0o600)
}

func (r *NativeRunner) clearPendingRollback() error {
	err := os.Remove(r.pendingRollback())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (r *NativeRunner) recoverPendingRollback(ctx context.Context, serial string) error {
	if _, err := os.Stat(r.pendingRollback()); errors.Is(err, os.ErrNotExist) {
		r.rollbackRetrySerial = ""
		r.rollbackRetryAt = time.Time{}
		r.rollbackRetryError = nil
		return nil
	} else if err != nil {
		return err
	}
	if !r.needsRestoration() {
		// Older releases marked every stopped device, even untouched ones.
		r.rollbackRetrySerial, r.rollbackRetryAt, r.rollbackRetryError = "", time.Time{}, nil
		return r.clearPendingRollback()
	}
	if serial == r.rollbackRetrySerial && time.Now().Before(r.rollbackRetryAt) {
		return r.rollbackRetryError
	}
	r.step("rollback", "Resuming deferred restoration after stopping without an emulator")
	if err := r.setupAndroid(ctx, serial, "rollback"); err != nil {
		if ctx.Err() == nil {
			r.rollbackRetrySerial = serial
			r.rollbackRetryAt = time.Now().Add(time.Minute)
			r.rollbackRetryError = err
		}
		return err
	}
	if _, err := r.shell(ctx, serial, "rm -f "+shellQuote("/data/data/"+r.cfg.Android.Package+"/files/UserPreferences/v1/MissionUserPrefs")); err != nil {
		if ctx.Err() == nil {
			r.rollbackRetrySerial = serial
			r.rollbackRetryAt = time.Now().Add(time.Minute)
			r.rollbackRetryError = err
		}
		return err
	}
	if err := r.clearPendingRollback(); err != nil {
		return err
	}
	r.rollbackRetrySerial = ""
	r.rollbackRetryAt = time.Time{}
	r.rollbackRetryError = nil
	return nil
}

func (r *NativeRunner) pidFile(name string) string {
	return filepath.Join(r.cfg.Runtime.RuntimeDirectory, name+".pid")
}

func (r *NativeRunner) libraryPath(ctx context.Context, serial, library string) (string, error) {
	if details, err := r.packageDetails(ctx, serial); err == nil {
		if match := regexp.MustCompile(`\bnativeLibraryDir=([^\s]+)`).FindStringSubmatch(details); len(match) == 2 {
			return path.Join(match[1], library), nil
		}
	}
	out, err := r.adb(ctx, serial, "shell", "pm", "path", r.cfg.Android.Package)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "package:") {
			return path.Join(path.Dir(strings.TrimPrefix(strings.TrimSpace(line), "package:")), "lib/arm64", library), nil
		}
	}
	return "", fmt.Errorf("package %s is missing", r.cfg.Android.Package)
}

func (r *NativeRunner) remoteHash(ctx context.Context, serial, filename string) (string, error) {
	out, err := r.shell(ctx, serial, "sha256sum "+shellQuote(filename))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 || !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(fields[0]) {
		return "", fmt.Errorf("invalid Android SHA-256")
	}
	return strings.ToLower(fields[0]), nil
}

// Action preserves verified backups and never starts the game.
func (r *NativeRunner) Action(ctx context.Context, action Action) (Status, error) {
	if _, ok := parseAction(string(action)); !ok {
		return Status{}, fmt.Errorf("unknown action %q", action)
	}
	// Any action can change routing, certificates, the native patch or process
	// state. If it fails, the next status request must inspect everything again.
	r.fullStatusValid = false
	r.beginActionCache()
	defer r.endActionCache()
	if action == ActionStop && !r.needsRestoration() {
		if status, done, err := r.stopUntouched(ctx); done {
			return status, err
		}
	}
	if action == ActionStop {
		if err := r.markPendingRollback(); err != nil {
			return Status{}, err
		}
	}
	serial := r.cachedSerial
	var err error
	if serial != "" {
		state, stateErr := r.adb(ctx, serial, "get-state")
		if stateErr != nil || state != "device" {
			serial = ""
			r.cachedSerial = ""
		}
	}
	if serial == "" {
		serial, err = r.discover(ctx)
		if err != nil {
			if action == ActionStop {
				if stopErr := r.stopProcess("server", r.server); stopErr != nil {
					return Status{}, stopErr
				}
				if modeErr := r.setMode("stopped"); modeErr != nil {
					return Status{}, modeErr
				}
				r.step("stop", "Emulator unavailable: shutdown completed, Android restoration deferred")
				return Status{
					Mode:   "stopped",
					Server: ServerStatus{Running: false},
					Android: AndroidStatus{
						Connected: false,
						Routing:   "restauration en attente",
						CA:        "restauration en attente",
						Native:    "restauration en attente",
						Game:      "émulateur arrêté",
					},
				}, nil
			}
			return Status{}, err
		}
	}
	if action == ActionLocal {
		if err := r.enableLocal(ctx, serial); err != nil {
			return Status{}, err
		}
	} else {
		err = r.setupAndroid(ctx, serial, "rollback")
		// Stop must shut down the server even when Android restoration fails.
		if err != nil && action == ActionOnline {
			return Status{}, err
		}
		err = errors.Join(err, r.stopProcess("server", r.server))
		if err != nil {
			return Status{}, err
		}
	}
	if _, err := r.shell(ctx, serial, "rm -f "+shellQuote("/data/data/"+r.cfg.Android.Package+"/files/UserPreferences/v1/MissionUserPrefs")); err != nil {
		return Status{}, err
	}
	if err := r.clearPendingRollback(); err != nil {
		return Status{}, err
	}
	mode := string(action)
	if action == ActionStop {
		mode = "stopped"
	}
	if err := r.setMode(mode); err != nil {
		return Status{}, err
	}
	if action == ActionStop {
		// The panel exits after Stop: a final inspection would be discarded.
		// The rollback above already verified the official library.
		return stoppedStatus(serial, true, "officiel"), nil
	}
	return r.fullStatus(ctx, serial)
}

// enableLocal checks the game, then starts the server while Android is
// configured: both are independent until the game connects. A device whose
// patch, certificate and routing are still in place only gets its tunnel.
func (r *NativeRunner) enableLocal(ctx context.Context, serial string) error {
	check := r.cfg
	check.Android.Serial = serial
	if _, err := android.Inspect(ctx, r.commands, check); err != nil {
		return err
	}
	program, err := installation.Program(r.root)
	if err != nil {
		return err
	}
	prepared, err := provision.Resolve(check, program)
	if err != nil {
		return err
	}
	r.cfg = prepared
	r.step("local", "Checking certificates and starting the server")
	if err := r.ensureCertificates(); err != nil {
		return err
	}
	started := r.processPID("server", r.server) == 0
	server := make(chan error, 1)
	go func() {
		server <- r.startProcess(ctx, "server", r.server, r.cfg.Runtime.Address, "serve", "--config", r.cfg.Path, "--adb-serial", serial, "--address", r.cfg.Runtime.Address, "--admin-address", r.cfg.Runtime.AdminAddress, "--traffic-log", r.cfg.Runtime.TrafficLog)
	}()
	var androidErr error
	if r.localStillApplied(ctx, serial) {
		r.step("local", "Android is still configured: restoring the ADB tunnel only")
		androidErr = r.restoreTunnel(ctx, serial)
	} else {
		androidErr = r.setupAndroid(ctx, serial, "apply")
	}
	err = errors.Join(androidErr, <-server)
	if err != nil && started {
		err = errors.Join(err, r.stopProcess("server", r.server))
	}
	return err
}

// ensureCertificates renews the local leaf in-process (formerly the
// `cert ensure` subcommand, started as a separate program on every switch).
func (r *NativeRunner) ensureCertificates() error {
	ca := r.cfg.Runtime.CertificateAuthority
	return localtls.EnsureFiles(localtls.Paths{CA: ca, CAKey: filepath.Join(filepath.Dir(ca), "ca-key.pem"), Certificate: r.cfg.Runtime.Certificate, PrivateKey: r.cfg.Runtime.PrivateKey}, r.cfg.Android.RedirectHosts)
}

// localStillApplied reports whether a previous Local left the verified patch,
// certificate and hosts routing in place (the panel or the server stopped,
// the emulator did not restart).
func (r *NativeRunner) localStillApplied(ctx context.Context, serial string) bool {
	if _, err := os.Stat(r.installedPatch()); err != nil {
		return false
	}
	manifest, err := r.manifest(false)
	if err != nil {
		return false
	}
	hosts, err := r.adb(ctx, serial, "shell", "cat", "/system/etc/hosts")
	if err != nil || !strings.Contains(hosts, "# PTCGP-LOCAL-BEGIN") {
		return false
	}
	library, err := r.libraryPath(ctx, serial, manifest.Library)
	if err != nil {
		return false
	}
	if hash, err := r.remoteHash(ctx, serial, library); err != nil || hash != manifest.TargetSHA256 {
		return false
	}
	name, err := localtls.AndroidSystemName(r.cfg.Runtime.CertificateAuthority)
	return err == nil && r.androidCAInstalled(ctx, serial, name)
}

func (r *NativeRunner) restoreTunnel(ctx context.Context, serial string) error {
	details, err := r.packageDetails(ctx, serial)
	if err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(r.cfg.Runtime.Address)
	if err != nil {
		return err
	}
	return r.configureTunnel(ctx, serial, port, details)
}

// stopUntouched stops a device that carries no local Android changes without
// discovering it: a missing emulator costs nothing, a connected one only has
// its game closed. It defers to the full restoration if local routing is
// unexpectedly present.
func (r *NativeRunner) stopUntouched(ctx context.Context) (Status, bool, error) {
	serial := r.cachedSerial
	if serial == "" {
		serial = r.cfg.Android.Serial
	}
	connected := false
	if serial != "" {
		state, err := r.adb(ctx, serial, "get-state")
		connected = err == nil && state == "device"
	}
	if connected {
		hosts, err := r.adb(ctx, serial, "shell", "cat", "/system/etc/hosts")
		if err != nil || strings.Contains(hosts, "# PTCGP-LOCAL-BEGIN") {
			return Status{}, false, nil
		}
	}
	if err := r.stopProcess("server", r.server); err != nil {
		return Status{}, true, err
	}
	if connected {
		if _, err := r.adb(ctx, serial, "shell", "am", "force-stop", r.cfg.Android.Package); err != nil {
			return Status{}, true, err
		}
	}
	if err := r.clearPendingRollback(); err != nil {
		return Status{}, true, err
	}
	if err := r.setMode("stopped"); err != nil {
		return Status{}, true, err
	}
	routing := "indisponible"
	if connected {
		routing = "officiel"
	}
	return stoppedStatus(serial, connected, routing), true, nil
}

func stoppedStatus(serial string, connected bool, routing string) Status {
	s := Status{Mode: "stopped", Android: AndroidStatus{Serial: serial, Connected: connected, Root: connected, Routing: routing, CA: "inconnue", Native: "inconnue", Game: "arrete"}}
	if connected {
		s.Android.CA, s.Android.Native = "officielle uniquement", "officiel"
	}
	return s
}

// StartPanel starts the persistent administration and optionally opens it.
func (r *NativeRunner) StartPanel(ctx context.Context, open bool) error {
	if err := r.startProcess(ctx, "launcher", r.launcher, r.cfg.Runtime.LauncherAddress, "serve-panel", "--config", r.cfg.Path, "--project-root", r.root); err != nil {
		return err
	}
	if open {
		// The desktop application is the control panel window.
		for _, desktop := range []string{os.Getenv("PTCGP_DESKTOP_EXE"), filepath.Join(r.root, provision.DesktopExecutable)} {
			if desktop == "" {
				continue
			}
			if info, err := os.Stat(desktop); err == nil && info.Mode().IsRegular() {
				return startDesktop(desktop, r.root)
			}
		}
		return fmt.Errorf("%s is missing next to the bin folder", provision.DesktopExecutable)
	}
	return nil
}

// startDesktop opens the native panel window that started this panel.
func startDesktop(executable, root string) error {
	cmd := exec.Command(executable)
	cmd.Dir = root
	if err := startWindowed(cmd); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// StopPanel stops a launcher started by this project.
func (r *NativeRunner) StopPanel() error {
	return r.stopProcess("launcher", r.launcher)
}

func loopbackAddress(address string) string {
	_, port, _ := net.SplitHostPort(address)
	return net.JoinHostPort("127.0.0.1", port)
}
