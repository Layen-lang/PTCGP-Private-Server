package control

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/updates"
)

func (h *Handler) SetUpdates(manager *updates.Manager, install func() error) {
	h.updates = manager
	h.installUpdate = install
}
func (h *Handler) updateAction(w http.ResponseWriter, r *http.Request) {
	if h.updates == nil || h.updates.PreparedKey() == "" {
		writeJSON(w, 409, map[string]string{"error": "No verified update is ready"})
		return
	}
	if !h.tryStartOperation("update") {
		writeJSON(w, 409, map[string]string{"error": "Another operation is in progress"})
		return
	}
	defer h.finishOperation()
	h.runnerMu.Lock()
	defer h.runnerMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if _, err := h.runner.Action(ctx, ActionStop); err != nil {
		writeJSON(w, 500, map[string]string{"error": fmt.Sprintf("Restore official mode before updating: %v", err)})
		return
	}
	if err := h.installUpdate(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]bool{"restarting": true})
	if h.shutdown != nil {
		h.shutdown()
	}
}
