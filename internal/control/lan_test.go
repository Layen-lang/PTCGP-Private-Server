package control

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestLANPanelKeepsCSRFAndOriginChecks(t *testing.T) {
	backend, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	panel := newHandler(t, &fakeRunner{}, backend)
	server := httptest.NewServer(LANHandler(panel, "127.0.0.1:8080"))
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/control/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "192.168.1.132:8082"
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	cookies := response.Cookies()
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(cookies) != 1 {
		t.Fatalf("status=%d cookies=%v", response.StatusCode, cookies)
	}
	for _, test := range []struct {
		name, origin string
		token        bool
		want         int
	}{
		{"no token", "http://192.168.1.132:8082", false, http.StatusForbidden},
		{"foreign origin", "http://evil.example", true, http.StatusForbidden},
		{"other LAN origin", "http://192.168.1.133:8082", true, http.StatusForbidden},
		{"opaque origin", "null", true, http.StatusForbidden},
		{"same origin", "http://192.168.1.132:8082", true, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL+"/api/control/actions/online", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = "192.168.1.132:8082"
			request.Header.Set("Origin", test.origin)
			if test.token {
				request.AddCookie(cookies[0])
				request.Header.Set("X-Control-CSRF-Token", cookies[0].Value)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.want {
				t.Fatalf("status=%d, want %d", response.StatusCode, test.want)
			}
		})
	}
}

func TestLANRejectsPublicClientsAndHostnames(t *testing.T) {
	for _, test := range []struct {
		name, host, remote, site string
		want                     int
	}{
		{"home network", "192.168.1.132:8082", "192.168.1.20:50000", "", http.StatusNoContent},
		{"public client", "192.168.1.132:8082", "8.8.8.8:50000", "", http.StatusForbidden},
		{"public host", "8.8.8.8:8082", "192.168.1.20:50000", "", http.StatusForbidden},
		{"hostname", "evil.example:8082", "192.168.1.20:50000", "", http.StatusForbidden},
		{"cross site", "192.168.1.132:8082", "192.168.1.20:50000", "cross-site", http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			panel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "127.0.0.1:8080" {
					t.Errorf("internal host=%q", r.Host)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/accounts", nil)
			request.RemoteAddr = test.remote
			request.Header.Set("Sec-Fetch-Site", test.site)
			response := httptest.NewRecorder()
			LANHandler(panel, "127.0.0.1:8080").ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d", response.Code, test.want)
			}
		})
	}
}
