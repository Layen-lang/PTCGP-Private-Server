package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/contracts"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/installation"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
)

// ActivateProfile requires identical executable bytes and the compiled protocol.
// The caller serializes this with local mode changes and keeps the server stopped.
func (m *Manager) ActivateProfile() error {
	m.work.Lock()
	defer m.work.Unlock()
	if !m.Status().ProfileOnly {
		return fmt.Errorf("update changes executable code")
	}
	key := m.PreparedKey()
	manifest, err := ValidatePrepared(m.root, key)
	if err != nil {
		return err
	}
	for _, file := range manifest.Files {
		if strings.HasPrefix(file.Path, "bin/") {
			data, e := os.ReadFile(filepath.Join(m.program, filepath.FromSlash(file.Path)))
			if e != nil || hash(data) != file.SHA256 {
				return fmt.Errorf("executable changed since profile verification")
			}
		}
	}
	program := filepath.Join(m.root, "data", "updates", "versions", key)
	cfg, err := configuration.Load(filepath.Join(program, "server.json"))
	if err != nil {
		return err
	}
	if _, err = contracts.VerifyProto(cfg.Contracts); err != nil {
		return err
	}
	var previous installation.Selection
	b, e := os.ReadFile(filepath.Join(m.root, "data", "updates", "current.json"))
	if e == nil {
		if e = json.Unmarshal(b, &previous); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if err = provision.WriteJSON(filepath.Join(m.root, "data", "updates", "current.json"), installation.Selection{Key: key, Previous: previous.Key, Version: manifest.Version}); err != nil {
		return err
	}
	m.mu.Lock()
	m.program = program
	m.state = State{Phase: "current", Version: manifest.Version, Message: "Profil de compatibilité actualisé"}
	m.mu.Unlock()
	return nil
}

func Copy(from, to string) error {
	in, e := os.Open(from)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	closeErr := out.Close()
	if e != nil {
		return e
	}
	return closeErr
}

// StartInstaller copies the current trusted executable so it can outlive the
// panel. The helper validates the signed release again after the panel exits.
func StartInstaller(root, key string) error {
	if _, e := ValidatePrepared(root, key); e != nil {
		return e
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	helper := filepath.Join(root, "data", "updates", "installer-"+strconv.Itoa(os.Getpid())+".exe")
	if e = Copy(exe, helper); e != nil {
		return e
	}
	cmd := exec.Command(helper, "apply-update", root, key, strconv.Itoa(os.Getpid()))
	hideWindow(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	return cmd.Process.Release()
}

// Apply runs only in the detached helper. Accounts are backed up after all
// processes have closed, before the replacement program can migrate SQLite.
func Apply(root, key string, parent int) error {
	if e := waitForExit(parent, 45*time.Second); e != nil {
		return e
	}
	manifest, e := ValidatePrepared(root, key)
	if e != nil {
		return e
	}
	cfg, e := configuration.Load(filepath.Join(root, "server.json"))
	if e != nil {
		return e
	}
	current := filepath.Join(root, "data", "updates", "current.json")
	previousBytes, previousErr := os.ReadFile(current)
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return previousErr
	}
	var previous installation.Selection
	_ = json.Unmarshal(previousBytes, &previous)
	oldProgram, e := installation.Program(root)
	if e != nil {
		return e
	}
	backup := filepath.Join(root, "data", "updates", "accounts-before-"+key+".db")
	hasDB := false
	if _, e = os.Stat(cfg.Runtime.Database); e == nil {
		if e = Copy(cfg.Runtime.Database, backup); e != nil {
			return e
		}
		hasDB = true
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = provision.WriteJSON(current, installation.Selection{Key: key, Previous: previous.Key, Version: manifest.Version}); e != nil {
		return e
	}
	program, e := installation.Program(root)
	if e != nil {
		return e
	}
	start := func(dir string) (*exec.Cmd, error) {
		cmd := exec.Command(filepath.Join(dir, "bin", "ptcgp-launcher.exe"), "serve-panel", "--config", filepath.Join(root, "server.json"), "--project-root", root)
		hideWindow(cmd)
		cmd.Dir = root
		return cmd, cmd.Start()
	}
	cmd, startErr := start(program)
	if startErr == nil {
		finished := make(chan error, 1)
		go func() { finished <- cmd.Wait() }()
		client := &http.Client{Timeout: time.Second}
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(300 * time.Millisecond)
		defer tick.Stop()
		waiting := true
		for waiting {
			select {
			case <-finished:
				waiting = false
				startErr = fmt.Errorf("updated panel exited during startup")
			case <-deadline.C:
				waiting = false
				startErr = fmt.Errorf("updated panel did not become healthy")
				_ = cmd.Process.Kill()
				<-finished
			case <-tick.C:
				response, err := client.Get("http://" + cfg.Runtime.LauncherAddress + "/api/control/health")
				if err == nil {
					var health struct {
						Healthy bool
						PID     int
					}
					decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health)
					response.Body.Close()
					if response.StatusCode == 200 && decodeErr == nil && health.Healthy && health.PID == cmd.Process.Pid {
						return nil
					}
				}
			}
		}
	}
	if previousErr == nil {
		if e = provision.WriteJSON(current, json.RawMessage(previousBytes)); e != nil {
			return e
		}
	} else {
		if e = os.Remove(current); e != nil {
			return e
		}
	}
	if hasDB {
		failedSuffix := ".failed-" + key + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		for _, suffix := range []string{"", "-wal", "-shm"} {
			p := cfg.Runtime.Database + suffix
			if _, e = os.Stat(p); e == nil {
				if e = os.Rename(p, p+failedSuffix); e != nil {
					return e
				}
			}
		}
		if e = Copy(backup, cfg.Runtime.Database); e != nil {
			return e
		}
	}
	_, e = start(oldProgram)
	if e != nil {
		return fmt.Errorf("update failed (%v); rollback startup: %w", startErr, e)
	}
	_ = provision.WriteJSON(filepath.Join(root, "data", "updates", "last-error.json"), map[string]string{"error": startErr.Error()})
	return startErr
}

// FollowInstalled redirects the original entry point into the active release.
// It only follows an installed selection, never a network response.
func FollowInstalled(args []string) (bool, error) {
	config, _, e := configuration.Arguments(args)
	if e != nil {
		return false, e
	}
	root, e := filepath.Abs(filepath.Dir(config))
	if e != nil {
		return false, e
	}
	program, e := installation.Program(root)
	if e != nil {
		return false, e
	}
	if program == root {
		return false, nil
	}
	exe, e := os.Executable()
	if e != nil {
		return false, e
	}
	target := filepath.Join(program, "bin", "ptcgp-launcher.exe")
	if strings.EqualFold(filepath.Clean(exe), filepath.Clean(target)) {
		return false, nil
	}
	cmd := exec.CommandContext(context.Background(), target, args...)
	hideWindow(cmd)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return true, cmd.Run()
}
