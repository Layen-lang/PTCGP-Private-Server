package control

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
)

// SetPreparation installs the one asynchronous preparation operation. The
// callback is serialized with mode changes and may replace the administration.
func (h *Handler) SetPreparation(manager *provision.Manager, prepare func(context.Context, string) error) {
	h.preparation = manager
	h.prepare = prepare
}
func (h *Handler) StartPreparation(serial string) bool {
	h.preparationMu.Lock()
	defer h.preparationMu.Unlock()
	if h.preparationClosed || h.prepare == nil || !h.tryStartOperation("prepare") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	h.preparationStop = cancel
	done := make(chan struct{})
	h.preparationDone = done
	go func() {
		defer close(done)
		defer cancel()
		defer h.finishOperation()
		h.runnerMu.Lock()
		defer h.runnerMu.Unlock()
		if err := h.prepare(ctx, serial); err != nil {
			h.preparation.Fail(err)
			h.journal.Record("prepare", "error", err.Error())
		}
	}()
	return true
}

// ClosePreparation cancels import work and waits before its database is closed.
func (h *Handler) ClosePreparation() {
	h.preparationMu.Lock()
	h.preparationClosed = true
	if h.preparationStop != nil {
		h.preparationStop()
	}
	done := h.preparationDone
	h.preparationMu.Unlock()
	if done != nil {
		<-done
	}
}
func (h *Handler) preparationAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Serial string `json:"serial"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			writeJSON(w, 400, map[string]string{"error": "Invalid device selection"})
			return
		}
	}
	if !h.StartPreparation(input.Serial) {
		writeJSON(w, 409, map[string]string{"error": "Another operation is in progress"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (r *NativeRunner) SetPreparedConfig(cfg configuration.Config) {
	r.cfg = cfg
	r.cachedSerial = cfg.Android.Serial
	r.fullStatusValid = false
}
