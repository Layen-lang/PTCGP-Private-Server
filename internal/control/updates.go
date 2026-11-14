package control

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// SetVersion records the installed program version reported by health.
func (h *Handler) SetVersion(version string) { h.version = version }

// updateAction prepares a program update. The desktop application downloads
// and installs releases; the backend only has to restore the official game
// and exit so that its own files can be replaced.
func (h *Handler) updateAction(w http.ResponseWriter, r *http.Request) {
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
		// This version can reject a library patched for a newer game. If
		// Android is already fully official and idle there is nothing to
		// restore, and the new version resumes with its own manifest.
		current, statusErr := h.runner.Status(ctx)
		if statusErr != nil || !strings.Contains(err.Error(), "unsupported native library") ||
			current.Server.Running || current.Android.Running || current.Android.Routing != "officiel" ||
			current.Android.CA != "officielle uniquement" {
			writeJSON(w, 500, map[string]string{"error": fmt.Sprintf("Restore official mode before updating: %v", err)})
			return
		}
	}
	writeJSON(w, 202, map[string]any{"restarting": true, "previousPID": os.Getpid()})
	if h.shutdown != nil {
		h.shutdown()
	}
}
