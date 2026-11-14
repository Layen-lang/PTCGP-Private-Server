package control

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/android"
)

// wirelessRunner runs ADB for phone pairing; tests replace it.
var wirelessRunner = android.SystemRunner

// wirelessAction pairs with or connects to a phone over Wi-Fi. ADB pairing and
// connection do not change the selected device or its configuration, so they
// do not wait for the device runner.
func (h *Handler) wirelessAction(w http.ResponseWriter, r *http.Request, pair bool) {
	var input struct {
		Address string `json:"address"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	var serial string
	var err error
	if pair {
		serial, err = android.WirelessPair(ctx, wirelessRunner(), input.Address, input.Code)
	} else {
		serial, err = android.WirelessConnect(ctx, wirelessRunner(), input.Address)
	}
	if err != nil {
		h.journal.Record("panel", "error", "Wi-Fi phone: "+err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	switch {
	case serial != "":
		h.journal.Record("panel", "success", "Wi-Fi phone connected: "+serial)
	case pair:
		h.journal.Record("panel", "success", "Wi-Fi phone paired: "+input.Address)
	}
	writeJSON(w, http.StatusOK, map[string]any{"paired": pair, "serial": serial})
}
