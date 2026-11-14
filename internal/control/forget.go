package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ManagedDevice is a device the panel remembers (data/runtime/emulators.json),
// connected or not. The desktop app lets the user forget obsolete ones.
type ManagedDevice struct {
	Serial             string `json:"serial"`
	Selected           bool   `json:"selected"`
	Running            bool   `json:"running"`
	NeedsRestoration   bool   `json:"needsRestoration"`
	PendingRestoration bool   `json:"pendingRestoration"`
}

// ManagedDevices reads local files only: it never contacts a device.
func (m *MultiRunner) ManagedDevices() []ManagedDevice {
	devices := make([]ManagedDevice, 0, len(m.runtimes))
	for serial, entry := range m.runtimes {
		directory := m.deviceConfig(entry).Runtime.RuntimeDirectory
		_, pendingErr := os.Stat(filepath.Join(directory, "pending-rollback"))
		device := ManagedDevice{Serial: serial, Selected: serial == m.selected, NeedsRestoration: runtimeNeedsRestoration(directory)}
		device.PendingRestoration = pendingErr == nil && device.NeedsRestoration
		if managed := m.devices[serial]; managed != nil {
			device.Running = managed.running()
		}
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Serial < devices[j].Serial })
	return devices
}

// ForgetDevice removes an obsolete device: its saved ports, runtime files and
// account reservation. A device that may still carry local Android changes is
// kept, so its restoration is never lost.
func (m *MultiRunner) ForgetDevice(ctx context.Context, serial string) error {
	entry, exists := m.runtimes[serial]
	if !exists {
		return fmt.Errorf("this device is not remembered")
	}
	if serial == m.selected {
		return fmt.Errorf("select another device before forgetting this one")
	}
	if device := m.devices[serial]; device != nil && device.running() {
		return fmt.Errorf("stop this device's server before forgetting it")
	}
	directory := m.deviceConfig(entry).Runtime.RuntimeDirectory
	if runtimeNeedsRestoration(directory) {
		return fmt.Errorf("this device still has local changes: reconnect it and switch it to Official before forgetting it")
	}
	if err := m.state.ReleaseEmulator(ctx, serial); err != nil {
		return err
	}
	delete(m.runtimes, serial)
	if err := m.saveRuntimes(); err != nil {
		m.runtimes[serial] = entry
		return err
	}
	delete(m.devices, serial)
	delete(m.pendingChecks, serial)
	m.publishRouting()
	// The first device shares the installation's runtime directory.
	if !entry.Legacy {
		if err := os.RemoveAll(directory); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) forgetAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Serial string `json:"serial"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.Serial == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose a device to forget"})
		return
	}
	devices, ok := h.runner.(interface {
		ForgetDevice(context.Context, string) error
		ManagedDevices() []ManagedDevice
	})
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "Device management is unavailable"})
		return
	}
	if !h.tryStartOperation("forget") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Another operation is in progress"})
		return
	}
	defer h.finishOperation()
	h.lockRunner("forget")
	defer h.runnerMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := devices.ForgetDevice(ctx, input.Serial); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	h.journal.Record("forget", "success", "Device forgotten: "+input.Serial)
	h.statusMu.Lock()
	h.last.Managed = devices.ManagedDevices()
	h.statusMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"managed": devices.ManagedDevices()})
}

// managedDevices lists remembered devices when the runner tracks several.
func (h *Handler) managedDevices() []ManagedDevice {
	if devices, ok := h.runner.(interface{ ManagedDevices() []ManagedDevice }); ok {
		return devices.ManagedDevices()
	}
	return nil
}
