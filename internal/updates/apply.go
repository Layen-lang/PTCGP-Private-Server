package updates

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/Layen-lang/PTCGP-Private-Server/internal/installation"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
)

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
	output, e := os.OpenFile(filepath.Join(root, "data", "updates", "installer.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer output.Close()
	cmd := exec.Command(helper, "apply-update", root, key, strconv.Itoa(os.Getpid()))
	cmd.Dir, cmd.Stdout, cmd.Stderr = root, output, output
	if e = startDetached(cmd); e != nil {
		return e
	}
	return cmd.Process.Release()
}

// Apply runs only in the detached helper. Accounts are backed up after all
// processes have closed, before the replacement program can migrate SQLite.
func Apply(root, key string, parent int) (result error) {
	defer func() {
		if result != nil {
			result = errors.Join(result, provision.WriteJSON(filepath.Join(root, "data", "updates", "last-error.json"), map[string]string{"error": result.Error()}))
		}
	}()
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
	stage := filepath.Join(root, "data", "updates", "versions", key)
	if _, e = configuration.Load(filepath.Join(stage, "server.json")); e != nil {
		return e
	}
	merged, e := releaseConfig(filepath.Join(root, "server.json"), filepath.Join(stage, "server.json"))
	if e != nil {
		return e
	}
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
	files, e := newReplacement(root, key)
	if e != nil {
		return e
	}
	for _, file := range manifest.Files {
		if e = files.save(file.Path); e != nil {
			return e
		}
	}
	if e = files.save("data/updates/current.json"); e != nil {
		return e
	}
	start := func(dir string) (*exec.Cmd, error) {
		cmd := exec.Command(filepath.Join(dir, "bin", "ptcgp-launcher.exe"), "serve-panel", "--config", filepath.Join(root, "server.json"), "--project-root", root)
		cmd.Dir = root
		if err := os.MkdirAll(cfg.Runtime.RuntimeDirectory, 0700); err != nil {
			return cmd, err
		}
		output, err := os.OpenFile(filepath.Join(cfg.Runtime.RuntimeDirectory, "launcher.stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return cmd, err
		}
		defer output.Close()
		cmd.Stdout, cmd.Stderr = output, output
		return cmd, startDetached(cmd)
	}
	rollback := func(cause error, restoreDB bool) error {
		if err := files.restore(); err != nil {
			return errors.Join(cause, err)
		}
		if restoreDB && hasDB {
			if err := restoreAccounts(cfg.Runtime.Database, backup, key); err != nil {
				return errors.Join(cause, err)
			}
		}
		if err := provision.WriteJSON(filepath.Join(root, "data", "updates", "last-error.json"), map[string]string{"error": cause.Error()}); err != nil {
			return errors.Join(cause, err)
		}
		cmd, err := start(oldProgram)
		if err != nil {
			return fmt.Errorf("update failed (%v); rollback startup: %w", cause, err)
		}
		if err = waitForPanel(cmd, cfg.Runtime.LauncherAddress); err != nil {
			return errors.Join(cause, fmt.Errorf("rollback startup: %w", err))
		}
		return cause
	}
	configFile := filepath.Join(files.backup, "release-config.json")
	if e = os.WriteFile(configFile, merged, 0600); e != nil {
		return rollback(e, false)
	}
	for _, file := range manifest.Files {
		from := filepath.Join(stage, filepath.FromSlash(file.Path))
		if file.Path == "server.json" {
			from = configFile
		}
		if e = replaceFile(from, filepath.Join(root, filepath.FromSlash(file.Path))); e != nil {
			return rollback(e, false)
		}
	}
	// Clear the legacy selector only after every root file has been replaced.
	if e = os.Remove(filepath.Join(root, "data", "updates", "current.json")); e != nil && !os.IsNotExist(e) {
		return rollback(e, false)
	}
	if e = os.Remove(filepath.Join(root, "data", "updates", "last-error.json")); e != nil && !os.IsNotExist(e) {
		return rollback(e, false)
	}
	cmd, e := start(root)
	if e == nil {
		e = waitForPanel(cmd, cfg.Runtime.LauncherAddress)
	}
	if e != nil {
		return rollback(e, true)
	}
	return nil
}

func restoreAccounts(database, backup, key string) error {
	failedSuffix := ".failed-" + key + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := database + suffix
		if _, err := os.Stat(p); err == nil {
			if err = os.Rename(p, p+failedSuffix); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return Copy(backup, database)
}

// waitForPanel accepts health only from the exact child it started.
func waitForPanel(cmd *exec.Cmd, address string) error {
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-finished:
			return fmt.Errorf("panel exited during startup: %v", err)
		case <-deadline.C:
			killErr := cmd.Process.Kill()
			if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				return fmt.Errorf("stop unresponsive panel: %w", killErr)
			}
			<-finished
			return fmt.Errorf("panel did not become healthy")
		case <-tick.C:
			response, err := client.Get("http://" + address + "/api/control/health")
			if err != nil {
				continue
			}
			var health struct {
				Healthy bool
				PID     int
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && health.Healthy && health.PID == cmd.Process.Pid {
				return nil
			}
		}
	}
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
