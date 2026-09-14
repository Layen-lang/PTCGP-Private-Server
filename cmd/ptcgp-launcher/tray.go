package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/control"
)

type trayActions struct {
	IconSVG []byte
	Open    func()
	Local   func()
	Online  func()
	Stop    func()
}

func requestTrayAction(ctx context.Context, address string, action control.Action) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar}
	statusRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/control/status", nil)
	if err != nil {
		return err
	}
	statusResponse, err := client.Do(statusRequest)
	if err != nil {
		return err
	}
	var status control.Status
	err = json.NewDecoder(io.LimitReader(statusResponse.Body, 1<<20)).Decode(&status)
	statusResponse.Body.Close()
	if err != nil {
		return err
	}
	if statusResponse.StatusCode != http.StatusOK || status.CSRFToken == "" {
		return fmt.Errorf("control panel status unavailable (HTTP %d)", statusResponse.StatusCode)
	}
	actionRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/api/control/actions/"+string(action), nil)
	if err != nil {
		return err
	}
	actionRequest.Header.Set("X-Control-CSRF-Token", status.CSRFToken)
	actionResponse, err := client.Do(actionRequest)
	if err != nil {
		return err
	}
	defer actionResponse.Body.Close()
	if actionResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(actionResponse.Body, 1<<16))
		return fmt.Errorf("action %s rejected (HTTP %d): %s", action, actionResponse.StatusCode, body)
	}
	_, err = io.Copy(io.Discard, actionResponse.Body)
	return err
}
