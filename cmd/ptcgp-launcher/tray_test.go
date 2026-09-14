package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/control"
)

func TestRequestTrayActionUsesPanelControlEndpoint(t *testing.T) {
	const token = "tray-token"
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/control/status":
			http.SetCookie(w, &http.Cookie{Name: "ptcgp_control_csrf", Value: token, Path: "/"})
			_ = json.NewEncoder(w).Encode(control.Status{CSRFToken: token, Mode: "stopped"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/control/actions/local":
			cookie, err := r.Cookie("ptcgp_control_csrf")
			if err != nil || cookie.Value != token || r.Header.Get("X-Control-CSRF-Token") != token {
				http.Error(w, "invalid token", http.StatusForbidden)
				return
			}
			called = true
			_ = json.NewEncoder(w).Encode(control.Status{Mode: "local"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := requestTrayAction(context.Background(), strings.TrimPrefix(server.URL, "http://"), control.ActionLocal); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("tray action was not sent")
	}
}
