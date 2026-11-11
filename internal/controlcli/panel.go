package controlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/control"
)

// panelAction runs an action through the running panel, which hosts the
// game servers. ok is false when no panel listens.
func panelAction(ctx context.Context, address, action string) (status control.Status, ok bool, err error) {
	conn, dialErr := net.DialTimeout("tcp", address, 300*time.Millisecond)
	if dialErr != nil {
		return status, false, nil
	}
	conn.Close()
	client := &http.Client{Timeout: 5 * time.Minute}
	base := "http://" + address
	get := func() (control.Status, error) {
		var value control.Status
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/control/status", nil)
		if err != nil {
			return value, err
		}
		response, err := client.Do(request)
		if err != nil {
			return value, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return value, fmt.Errorf("panel status: HTTP %d", response.StatusCode)
		}
		return value, json.NewDecoder(response.Body).Decode(&value)
	}
	current, err := get()
	if err != nil || action == "status" {
		return current, true, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/control/actions/"+action, nil)
	if err != nil {
		return status, true, err
	}
	request.Header.Set("X-Control-CSRF-Token", current.CSRFToken)
	request.AddCookie(&http.Cookie{Name: "ptcgp_control_csrf", Value: current.CSRFToken})
	response, err := client.Do(request)
	if err != nil {
		return status, true, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return status, true, fmt.Errorf("another operation is in progress")
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(response.Body).Decode(&failure)
		return status, true, fmt.Errorf("%s", failure.Error)
	}
	return status, true, json.NewDecoder(response.Body).Decode(&status)
}
