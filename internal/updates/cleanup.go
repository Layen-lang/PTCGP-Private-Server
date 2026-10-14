package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/installation"
)

const updateLogLimit = 1 << 20

type cleanupPlan struct {
	Version string   `json:"version"`
	PID     int      `json:"pid"`
	Paths   []string `json:"paths"`
}

// planCleanup snapshots obsolete artifacts before the new panel can download
// another release. The current file backup and account backup are retained.
func planCleanup(root, version, backup, accounts string) (cleanupPlan, error) {
	plan := cleanupPlan{Version: version}
	if exe, err := os.Executable(); err == nil && installerName(filepath.Base(exe)) {
		plan.PID = os.Getpid()
	}
	base := filepath.Join(root, "data", "updates")
	entries, err := os.ReadDir(base)
	if err != nil {
		return plan, err
	}
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		if path == backup || path == accounts {
			continue
		}
		if cleanupPath(entry.Name()) {
			plan.Paths = append(plan.Paths, entry.Name())
		}
	}
	versions, err := os.ReadDir(filepath.Join(base, "versions"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, err
	}
	for _, entry := range versions {
		if installation.ValidKey(entry.Name()) {
			plan.Paths = append(plan.Paths, "versions/"+entry.Name())
		}
	}
	return plan, nil
}

func installerName(name string) bool {
	if !strings.HasPrefix(name, "installer-") || !strings.HasSuffix(name, ".exe") {
		return false
	}
	pid := strings.TrimSuffix(strings.TrimPrefix(name, "installer-"), ".exe")
	return pid != "" && strings.Trim(pid, "0123456789") == ""
}

// cleanupPath accepts only artifacts created by the updater, never root files.
func cleanupPath(path string) bool {
	if !filepath.IsLocal(path) || strings.ContainsAny(path, ":\\") || filepath.ToSlash(filepath.Clean(path)) != path {
		return false
	}
	if strings.HasPrefix(path, "versions/") {
		return installation.ValidKey(strings.TrimPrefix(path, "versions/"))
	}
	if installerName(path) {
		return true
	}
	if strings.HasPrefix(path, "accounts-before-") && strings.HasSuffix(path, ".db") {
		return installation.ValidKey(strings.TrimSuffix(strings.TrimPrefix(path, "accounts-before-"), ".db"))
	}
	if strings.HasPrefix(path, "backup-") {
		parts := strings.Split(strings.TrimPrefix(path, "backup-"), "-")
		return len(parts) == 2 && len(parts[0]) == 12 && strings.Trim(parts[0], "0123456789abcdef") == "" && parts[1] != "" && strings.Trim(parts[1], "0123456789") == ""
	}
	return false
}

// Cleanup finishes a successful installation after its helper has exited.
// It watches briefly because the panel starts before Apply confirms its health.
// A failed cleanup remains queued for the next launcher startup.
func (m *Manager) Cleanup(ctx context.Context) {
	root := m.root
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	deadline := time.NewTimer(time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
			data, err := os.ReadFile(filepath.Join(root, "data", "updates", "cleanup.json"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err == nil {
				var plan cleanupPlan
				err = json.Unmarshal(data, &plan)
				if err == nil && plan.PID != 0 {
					err = waitForExit(plan.PID, 45*time.Second)
				}
				if err == nil {
					// Discovery can reuse an old download directory. Serialize
					// cleanup with downloads and retain any newly prepared release.
					m.work.Lock()
					if key := m.PreparedKey(); key != "" {
						paths := plan.Paths[:0]
						for _, path := range plan.Paths {
							if path != "versions/"+key {
								paths = append(paths, path)
							}
						}
						plan.Paths = paths
					}
					err = finishCleanup(root, plan)
					m.work.Unlock()
				}
			}
			if err != nil {
				slog.Warn("update cleanup deferred", "error", err)
			}
			return
		}
	}
}

func finishCleanup(root string, plan cleanupPlan) error {
	version, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return err
	}
	if _, valid := releaseVersion(plan.Version); !valid || strings.TrimSpace(string(version)) != plan.Version {
		return fmt.Errorf("cleanup does not match installed version")
	}
	base, err := filepath.EvalSymlinks(filepath.Join(root, "data", "updates"))
	if err != nil {
		return err
	}
	// Validate the entire plan before deleting anything.
	for _, path := range plan.Paths {
		if !cleanupPath(path) {
			return fmt.Errorf("invalid cleanup path: %s", path)
		}
		target := filepath.Join(base, filepath.FromSlash(path))
		resolved, err := filepath.EvalSymlinks(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(target)) {
			return fmt.Errorf("cleanup target is a redirected path: %s", path)
		}
	}
	for _, path := range plan.Paths {
		if err := os.RemoveAll(filepath.Join(base, filepath.FromSlash(path))); err != nil {
			return err
		}
	}
	if err := trimUpdateLog(filepath.Join(base, "installer.log")); err != nil {
		return err
	}
	return os.Remove(filepath.Join(base, "cleanup.json"))
}

// trimUpdateLog retains at most the latest MiB without creating another log.
func trimUpdateLog(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= updateLogLimit {
		return err
	}
	tail := make([]byte, updateLogLimit)
	if _, err = file.ReadAt(tail, info.Size()-updateLogLimit); err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err = file.Write(tail); err != nil {
		return err
	}
	return file.Truncate(int64(len(tail)))
}
