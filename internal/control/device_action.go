package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
)

func (h *Handler) deviceAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Serial string `json:"serial"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.Serial == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Choose a connected emulator"})
		return
	}
	if !h.tryStartOperation("device") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Another operation is in progress"})
		return
	}
	defer h.finishOperation()
	h.runnerMu.Lock()
	defer h.runnerMu.Unlock()
	devices, ok := h.runner.(interface {
		SelectDevice(context.Context, string) (Status, error)
	})
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "Device selection is unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	s, err := devices.SelectDevice(ctx, input.Serial)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.Managed = h.managedDevices()
	h.storeStatus(s)
	if h.preparation != nil {
		p := h.preparation.Status()
		s.Preparation = &p
	}
	if h.updates != nil {
		u := h.updates.Status()
		s.Update = &u
	}
	s.CSRFToken = h.csrf
	writeJSON(w, http.StatusOK, s)
}

// OpenPlayer serializes the account reservation and launch with mode changes
// and device selection, so an account can never be sent to a different device.
func (h *Handler) OpenPlayer(ctx context.Context, playerID string) error {
	if !h.tryStartOperation("open") {
		return fmt.Errorf("another operation is in progress")
	}
	defer h.finishOperation()
	started := time.Now()
	h.lockRunner("open")
	defer h.runnerMu.Unlock()
	devices, ok := h.runner.(interface {
		OpenPlayer(context.Context, string) error
	})
	if !ok {
		return fmt.Errorf("device account launching is unavailable")
	}
	if routing, ok := h.runner.(interface{ Routing() (string, string) }); ok {
		if requested := player.Emulator(ctx); requested != "" {
			if selected, _ := routing.Routing(); selected != requested {
				return fmt.Errorf("the selected emulator changed; try again")
			}
		}
	}
	if err := h.ensureLocal(ctx); err != nil {
		h.journal.Record("open", "error", err.Error()+" ("+elapsed(started)+")")
		return err
	}
	h.journal.Record("open", "info", "Launching the account in the game")
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := devices.OpenPlayer(ctx, playerID); err != nil {
		h.journal.Record("open", "error", err.Error()+" ("+elapsed(started)+")")
		return err
	}
	h.journal.Record("open", "success", "Account launched on the selected emulator in "+elapsed(started))
	return nil
}

// ensureLocal enables Local on the selected device when needed, so launching
// an account is a single step. The switch runs to completion even if the
// client gives up waiting: interrupting it could leave Android half set up.
func (h *Handler) ensureLocal(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	status, err := h.runner.Status(ctx)
	if err != nil {
		return err
	}
	if status.Mode == "local" && status.Server.Running && status.Android.Connected {
		return nil
	}
	if !status.Android.Connected {
		return fmt.Errorf("connect the selected emulator before launching an account")
	}
	if h.preparation != nil && !h.preparation.Status().Ready {
		return fmt.Errorf("complete data preparation before enabling local mode")
	}
	h.journal.Record("local", "info", "Enabling local mode before launching the account")
	status, err = h.runner.Action(ctx, ActionLocal)
	if err != nil {
		return err
	}
	status.Managed = h.managedDevices()
	h.storeStatus(status)
	return nil
}

// TrafficHistory reads the history path without racing device selection.
func (h *Handler) TrafficHistory(ctx context.Context) string {
	if devices, ok := h.runner.(interface{ TrafficHistory(string) string }); ok {
		return devices.TrafficHistory(player.Emulator(ctx))
	}
	return ""
}
