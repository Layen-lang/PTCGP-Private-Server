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
	"github.com/Layen-lang/PTCGP-Private-Server/internal/localtls"
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
	r := &NativeRunner{cfg: cfg, root: root, commands: nativeCommands{},
		server: filepath.Join(root, "bin", "ptcgp-server.exe"), launcher: filepath.Join(root, "bin", "ptcgp-launcher.exe")}
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
	return r.adb(ctx, serial, "shell", "su -c "+shellQuote(command))
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (r *NativeRunner) discover(ctx context.Context) (string, error) {
	return android.Discover(ctx, r.commands, r.cfg.Android.Serial, r.cfg.Android.Package)
}

// Status performs one complete inspection, then limits routine polling to the
// remembered device's connection state and game process. A failed connection
// invalidates the cache and triggers discovery again.
func (r *NativeRunner) Status(ctx context.Context) (Status, error) {
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
	s.Server.PID = managedPID(r.pidFile("server"), r.server)
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

func (r *NativeRunner) fullStatus(ctx context.Context, serial string) (Status, error) {
	s := Status{Mode: "unknown", Server: ServerStatus{PID: managedPID(r.pidFile("server"), r.server)},
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
			r.cachedSerial = ""
			r.fullStatusValid = false
			if ctx.Err() != nil {
				return s, ctx.Err()
			}
			return s, nil
		}
	}
	if err := r.recoverPendingRollback(ctx, serial); err != nil {
		r.fullStatusValid = false
		return s, err
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
		if _, err := r.shell(ctx, serial, "test -f "+shellQuote("/system/etc/security/cacerts/"+name)); err == nil {
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
		return nil
	} else if err != nil {
		return err
	}
	r.step("rollback", "Resuming deferred restoration after stopping without an emulator")
	if err := r.setupAndroid(ctx, serial, "rollback"); err != nil {
		return err
	}
	if _, err := r.shell(ctx, serial, "rm -f "+shellQuote("/data/data/"+r.cfg.Android.Package+"/files/UserPreferences/v1/MissionUserPrefs")); err != nil {
		return err
	}
	return r.clearPendingRollback()
}

func (r *NativeRunner) pidFile(name string) string {
	return filepath.Join(r.cfg.Runtime.RuntimeDirectory, name+".pid")
}

func (r *NativeRunner) libraryPath(ctx context.Context, serial, library string) (string, error) {
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

// Action preserves verified backups and only starts the game in official mode.
func (r *NativeRunner) Action(ctx context.Context, action Action) (Status, error) {
	if _, ok := parseAction(string(action)); !ok {
		return Status{}, fmt.Errorf("unknown action %q", action)
	}
	// Any action can change routing, certificates, the native patch or process
	// state. If it fails, the next status request must inspect everything again.
	r.fullStatusValid = false
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
		r.step("local", "Checking certificates and starting the server")
		if _, err := r.commands.Run(ctx, r.server, "cert", "ensure", "--config", r.cfg.Path); err != nil {
			return Status{}, err
		}
		started := managedPID(r.pidFile("server"), r.server) == 0
		if err := r.startProcess(ctx, "server", r.server, r.cfg.Runtime.Address, "serve", "--config", r.cfg.Path, "--adb-serial", serial); err != nil {
			return Status{}, err
		}
		if err := r.setupAndroid(ctx, serial, "apply"); err != nil {
			if started {
				err = errors.Join(err, r.stopProcess("server", r.server))
			}
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
	return r.fullStatus(ctx, serial)
}

// StartPanel starts the persistent administration and optionally opens it.
func (r *NativeRunner) StartPanel(ctx context.Context, open bool) error {
	if err := r.startProcess(ctx, "launcher", r.launcher, r.cfg.Runtime.LauncherAddress, "serve-panel", "--config", r.cfg.Path, "--project-root", r.root); err != nil {
		return err
	}
	if open {
		return openBrowser("http://" + r.cfg.Runtime.LauncherAddress)
	}
	return nil
}

// StopPanel stops a launcher started by this project.
func (r *NativeRunner) StopPanel() error {
	return r.stopProcess("launcher", r.launcher)
}

func loopbackAddress(address string) string {
	_, port, _ := net.SplitHostPort(address)
	return net.JoinHostPort("127.0.0.1", port)
}
