// Package control serves the persistent administration, operation journal,
// and game traffic proxy independently of the game server lifecycle.
package control

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/android"
)

const maxBodyBytes = 1 << 16

type Status struct {
	CSRFToken string        `json:"csrfToken,omitempty"`
	Busy      bool          `json:"busy"`
	Operation string        `json:"operation,omitempty"`
	Mode      string        `json:"mode"`
	Server    ServerStatus  `json:"server"`
	Android   AndroidStatus `json:"android"`
}

type ServerStatus struct {
	Running bool `json:"running"`
	PID     int  `json:"pid,omitempty"`
}

type AndroidStatus struct {
	Serial    string `json:"serial,omitempty"`
	Connected bool   `json:"connected"`
	Root      bool   `json:"root"`
	Routing   string `json:"routing"`
	CA        string `json:"ca"`
	Native    string `json:"native"`
	Game      string `json:"game"`
	Running   bool   `json:"running"`
}

type Action string

const (
	ActionLocal  Action = "local"
	ActionOnline Action = "online"
	ActionStop   Action = "stop"
)

type Runner interface {
	Status(context.Context) (Status, error)
	Action(context.Context, Action) (Status, error)
}

type Handler struct {
	runner          Runner
	proxy           *httputil.ReverseProxy
	static          http.Handler
	csrf            string
	administration  http.Handler
	journal         *Journal
	androidPackage  string
	androidActivity string
	shutdown        func()

	operationMu sync.RWMutex
	operation   string
	runnerMu    sync.Mutex
	statusMu    sync.RWMutex
	last        Status
	hasLast     bool
}

func New(frontend fs.FS, backend *url.URL, runner Runner) (*Handler, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(backend)
	handler := &Handler{
		runner: runner,
		proxy:  proxy,
		static: http.FileServer(http.FS(frontend)),
		csrf:   token,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, _ error) {
		if handler.administration != nil {
			handler.administration.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Administration unavailable."})
	}
	return handler, nil
}

// SetAdministration keeps account and catalog operations independent of the game process.
// Configure it before serving requests.
func (h *Handler) SetAdministration(handler http.Handler, journal *Journal, packageName, activity string) {
	h.administration, h.journal = handler, journal
	h.androidPackage, h.androidActivity = packageName, activity
}

// SetShutdown configures the graceful panel shutdown requested after a
// successful stop action. Configure it before serving requests.
func (h *Handler) SetShutdown(shutdown func()) {
	h.shutdown = shutdown
}

// Open launches an explicitly selected account, serialized with mode changes.
func (h *Handler) Open(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if !h.tryStartOperation("open") {
		return fmt.Errorf("an operation is already in progress")
	}
	defer h.finishOperation()
	h.runnerMu.Lock()
	defer h.runnerMu.Unlock()
	h.journal.Record("open", "info", "Checking the server and emulator before launching the account")
	status, err := h.runner.Status(ctx)
	if err == nil && (!status.Server.Running || status.Mode != "local") {
		err = fmt.Errorf("enable local mode before launching this account")
	}
	if err == nil {
		err = android.New(status.Android.Serial, h.androidPackage, h.androidActivity).Open(ctx)
	}
	if err != nil {
		h.journal.Record("open", "error", err.Error())
		return err
	}
	h.journal.Record("open", "success", "Account launched in the game")
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !loopbackHost(r.Host) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Panneau disponible uniquement sur 127.0.0.1."})
		return
	}
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")

	switchPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/control/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/control/status":
		h.status(w, r)
	case r.Method == http.MethodGet && (r.URL.Path == "/api/control/logs" || r.URL.Path == "/api/control/logs/export"):
		w.Header().Set("Cache-Control", "no-store")
		events, err := h.journal.Recent()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		diagnostics, err := h.journal.Diagnostics()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/export") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="ptcgp-logs.txt"`)
			var output strings.Builder
			for _, event := range events {
				fmt.Fprintf(&output, "%s [%s] [%s] %s\n", event.Time.Format(time.RFC3339), event.Level, event.Operation, event.Message)
			}
			output.WriteString("\nTechnical server output\n")
			output.WriteString(strings.Join(diagnostics, "\n"))
			_, _ = io.WriteString(w, output.String())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events, "diagnostics": diagnostics})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/control/actions/"):
		if h.validateMutation(w, r) {
			h.action(w, r, strings.TrimPrefix(switchPath, "actions/"))
		}
	case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/assets/image/"):
		if h.administration != nil && !strings.HasPrefix(r.URL.Path, "/api/traffic") {
			h.administration.ServeHTTP(w, r)
		} else {
			h.proxy.ServeHTTP(w, r)
		}
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed."})
	case !strings.Contains(strings.TrimPrefix(r.URL.Path, "/"), "."):
		clone := r.Clone(r.Context())
		clone.URL.Path = "/"
		h.static.ServeHTTP(w, clone)
	default:
		h.static.ServeHTTP(w, r)
	}
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h.setCSRFCookie(w)
	if operation := h.currentOperation(); operation != "" {
		h.writeBusyStatus(w, operation)
		return
	}
	h.runnerMu.Lock()
	defer h.runnerMu.Unlock()
	if operation := h.currentOperation(); operation != "" {
		h.writeBusyStatus(w, operation)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	status, err := h.runner.Status(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	h.storeStatus(status)
	status.CSRFToken = h.csrf
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) action(w http.ResponseWriter, _ *http.Request, rawAction string) {
	action, ok := parseAction(rawAction)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown control action."})
		return
	}
	if !h.tryStartOperation(string(action)) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Another operation is already in progress."})
		return
	}
	defer h.finishOperation()
	h.journal.Record(string(action), "info", "Starting operation: "+string(action))
	h.runnerMu.Lock()
	defer h.runnerMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := h.runner.Action(ctx, action)
	if err != nil {
		h.journal.Record(string(action), "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.storeStatus(status)
	h.journal.Record(string(action), "success", "Operation completed: "+string(action))
	status.CSRFToken = h.csrf
	writeJSON(w, http.StatusOK, status)
	if action == ActionStop && h.shutdown != nil {
		h.shutdown()
	}
}

func parseAction(value string) (Action, bool) {
	action := Action(value)
	switch action {
	case ActionLocal, ActionOnline, ActionStop:
		return action, true
	default:
		return "", false
	}
}

func (h *Handler) writeBusyStatus(w http.ResponseWriter, operation string) {
	status, ok := h.cachedStatus()
	if !ok {
		status = Status{
			Mode: "unknown",
			Android: AndroidStatus{
				Routing: "indisponible",
				CA:      "inconnue",
				Native:  "inconnue",
				Game:    "inconnu",
			},
		}
	}
	status.CSRFToken = h.csrf
	status.Busy = true
	status.Operation = operation
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) tryStartOperation(operation string) bool {
	h.operationMu.Lock()
	defer h.operationMu.Unlock()
	if h.operation != "" {
		return false
	}
	h.operation = operation
	return true
}

func (h *Handler) finishOperation() {
	h.operationMu.Lock()
	h.operation = ""
	h.operationMu.Unlock()
}

func (h *Handler) currentOperation() string {
	h.operationMu.RLock()
	defer h.operationMu.RUnlock()
	return h.operation
}

func (h *Handler) storeStatus(status Status) {
	status.CSRFToken = ""
	status.Busy = false
	status.Operation = ""
	h.statusMu.Lock()
	h.last = status
	h.hasLast = true
	h.statusMu.Unlock()
}

func (h *Handler) cachedStatus() (Status, bool) {
	h.statusMu.RLock()
	defer h.statusMu.RUnlock()
	return h.last, h.hasLast
}

func (h *Handler) validateMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength > maxBodyBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Request body is too large."})
		return false
	}
	if origin := strings.TrimSuffix(r.Header.Get("Origin"), "/"); origin != "" && origin != "null" && origin != "http://"+r.Host {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Origin rejected."})
		return false
	}
	if r.Header.Get("Origin") == "null" && r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Origin rejected."})
		return false
	}
	cookie, err := r.Cookie("ptcgp_control_csrf")
	header := r.Header.Get("X-Control-CSRF-Token")
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(h.csrf)) != 1 || subtle.ConstantTimeCompare([]byte(header), []byte(h.csrf)) != 1 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Invalid security token. Refresh the page."})
		return false
	}
	return true
}

func (h *Handler) setCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "ptcgp_control_csrf", Value: h.csrf, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", fmt.Errorf("generate control token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	return err == nil && host == "127.0.0.1"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
