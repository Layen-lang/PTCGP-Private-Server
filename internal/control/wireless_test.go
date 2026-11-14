package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/android"
)

type adbConnectRunner struct{ calls []string }

func (r *adbConnectRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	return []byte("connected to " + args[len(args)-1]), nil
}

func TestWirelessConnectRequiresTokenAndCallsADB(t *testing.T) {
	adb := &adbConnectRunner{}
	previous := wirelessRunner
	wirelessRunner = func() android.Runner { return adb }
	t.Cleanup(func() { wirelessRunner = previous })
	backend, _ := url.Parse("http://127.0.0.1:1")
	handler := newHandler(t, &fakeRunner{}, backend)
	send := func(token bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/control/wireless/connect", strings.NewReader(`{"address":"192.168.1.50:41815"}`))
		request.Host = "127.0.0.1:8080"
		if token {
			request.AddCookie(&http.Cookie{Name: "ptcgp_control_csrf", Value: handler.csrf})
			request.Header.Set("X-Control-CSRF-Token", handler.csrf)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := send(false); response.Code != http.StatusForbidden || len(adb.calls) != 0 {
		t.Fatalf("without token: status=%d calls=%v", response.Code, adb.calls)
	}
	response := send(true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"serial":"192.168.1.50:41815"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(adb.calls) != 1 || adb.calls[0] != "connect 192.168.1.50:41815" {
		t.Fatalf("calls=%v", adb.calls)
	}
}
