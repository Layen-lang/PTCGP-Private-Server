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
	h.runnerMu.Lock()
	defer h.runnerMu.Unlock()
	devices, ok := h.runner.(interface {
		OpenPlayer(context.Context, string) error
	})
	if !ok {
		return fmt.Errorf("device account launching is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := devices.OpenPlayer(ctx, playerID); err != nil {
		h.journal.Record("open", "error", err.Error())
		return err
	}
	h.journal.Record("open", "success", "Account launched on the selected emulator")
	return nil
}

// TrafficHistory reads the history path without racing device selection.
func (h *Handler) TrafficHistory(ctx context.Context) string {
	if devices, ok := h.runner.(interface{ TrafficHistory(string) string }); ok {
		return devices.TrafficHistory(player.Emulator(ctx))
	}
	return ""
}
