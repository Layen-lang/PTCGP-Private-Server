package control

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
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
		// Older panels can reject a library patched for the exact game version
		// in the verified update. If Android is already fully official and idle,
		// let the replacement panel resume the rollback with its newer manifest.
		current, statusErr := h.runner.Status(ctx)
		nativeHash := strings.TrimSuffix(strings.TrimPrefix(current.Android.Native, "inconnu ("), ")")
		if statusErr != nil || !strings.Contains(err.Error(), "unsupported native library") ||
			current.Server.Running || current.Android.Running || current.Android.Routing != "officiel" ||
			current.Android.CA != "officielle uniquement" || nativeHash == current.Android.Native ||
			!h.updates.PreparedSupportsNativeHash(nativeHash) {
			writeJSON(w, 500, map[string]string{"error": fmt.Sprintf("Restore official mode before updating: %v", err)})
			return
		}
	}
	if err := h.installUpdate(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"restarting": true, "previousPID": os.Getpid()})
	if h.shutdown != nil {
		h.shutdown()
	}
}
