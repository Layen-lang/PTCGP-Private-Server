package control

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeRunner struct {
	status  Status
	actions []string
	err     error
}

type blockingRunner struct {
	status  Status
	started chan struct{}
	release chan struct{}
}

func (b *blockingRunner) Status(context.Context) (Status, error) { return b.status, nil }
func (b *blockingRunner) Action(ctx context.Context, _ Action) (Status, error) {
	close(b.started)
	select {
	case <-b.release:
		return b.status, nil
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
}

func (f *fakeRunner) Status(context.Context) (Status, error) { return f.status, f.err }
func (f *fakeRunner) Action(_ context.Context, action Action) (Status, error) {
	f.actions = append(f.actions, string(action))
	return f.status, f.err
}

func newHandler(t *testing.T, runner Runner, backend *url.URL) *Handler {
	t.Helper()
	frontend := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("PTCGP control panel")}}
	handler, err := New(fs.FS(frontend), backend, runner)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestStatusSetsTokenAndActionRequiresIt(t *testing.T) {
	t.Parallel()
	backend, _ := url.Parse("http://127.0.0.1:1")
	runner := &fakeRunner{status: Status{Mode: "local", Server: ServerStatus{Running: true, PID: 42}}}
	handler := newHandler(t, runner, backend)

	statusRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/control/status", nil)
	statusRequest.Host = "127.0.0.1:8080"
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"csrfToken"`) {
		t.Fatalf("status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	cookies := statusResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "ptcgp_control_csrf" {
		t.Fatalf("cookies=%v", cookies)
	}

	denied := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/control/actions/online", nil)
	denied.Host = "127.0.0.1:8080"
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("missing token status=%d", deniedResponse.Code)
	}

	allowed := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/control/actions/online", nil)
	allowed.Host = "127.0.0.1:8080"
	allowed.AddCookie(cookies[0])
	allowed.Header.Set("X-Control-CSRF-Token", handler.csrf)
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusOK || len(runner.actions) != 1 || runner.actions[0] != "online" {
		t.Fatalf("status=%d actions=%v body=%s", allowedResponse.Code, runner.actions, allowedResponse.Body.String())
	}
}

func TestOperationRemainsVisibleAndLockedUntilRunnerCompletes(t *testing.T) {
	t.Parallel()
	backend, _ := url.Parse("http://127.0.0.1:1")
	runner := &blockingRunner{
		status:  Status{Mode: "local", Server: ServerStatus{Running: true, PID: 42}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	handler := newHandler(t, runner, backend)

	statusRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/control/status", nil)
	statusRequest.Host = "127.0.0.1:8080"
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	cookie := statusResponse.Result().Cookies()[0]

	newActionRequest := func(action string) *http.Request {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/control/actions/"+action, nil)
		request.Host = "127.0.0.1:8080"
		request.AddCookie(cookie)
		request.Header.Set("X-Control-CSRF-Token", handler.csrf)
		return request
	}

	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(firstResponse, newActionRequest("local"))
		close(firstDone)
	}()
	<-runner.started

	busyRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/control/status", nil)
	busyRequest.Host = "127.0.0.1:8080"
	busyResponse := httptest.NewRecorder()
	handler.ServeHTTP(busyResponse, busyRequest)
	if busyResponse.Code != http.StatusOK || !strings.Contains(busyResponse.Body.String(), `"busy":true`) || !strings.Contains(busyResponse.Body.String(), `"operation":"local"`) {
		t.Fatalf("busy status=%d body=%s", busyResponse.Code, busyResponse.Body.String())
	}

	conflictResponse := httptest.NewRecorder()
	handler.ServeHTTP(conflictResponse, newActionRequest("online"))
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("concurrent action status=%d body=%s", conflictResponse.Code, conflictResponse.Body.String())
	}

	close(runner.release)
	<-firstDone
	if firstResponse.Code != http.StatusOK || strings.Contains(firstResponse.Body.String(), `"busy":true`) {
		t.Fatalf("completed action status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}

	completedRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/control/status", nil)
	completedRequest.Host = "127.0.0.1:8080"
	completedResponse := httptest.NewRecorder()
	handler.ServeHTTP(completedResponse, completedRequest)
	if completedResponse.Code != http.StatusOK || !strings.Contains(completedResponse.Body.String(), `"busy":false`) {
		t.Fatalf("completed status=%d body=%s", completedResponse.Code, completedResponse.Body.String())
	}
}

func TestStopRequestsPanelShutdownAfterRestoration(t *testing.T) {
	t.Parallel()
	backend, _ := url.Parse("http://127.0.0.1:1")
	runner := &fakeRunner{status: Status{Mode: "stopped"}}
	handler := newHandler(t, runner, backend)
	shutdown := make(chan struct{}, 1)
	handler.SetShutdown(func() { shutdown <- struct{}{} })

	statusRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/control/status", nil)
	statusRequest.Host = "127.0.0.1:8080"
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/control/actions/stop", nil)
	request.Host = "127.0.0.1:8080"
	request.AddCookie(statusResponse.Result().Cookies()[0])
	request.Header.Set("X-Control-CSRF-Token", handler.csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mode":"stopped"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(runner.actions) != 1 || runner.actions[0] != "stop" {
		t.Fatalf("actions=%v", runner.actions)
	}
	select {
	case <-shutdown:
	default:
		t.Fatal("panel shutdown was not requested")
	}
}

func TestFailedStopDoesNotRequestPanelShutdown(t *testing.T) {
	t.Parallel()
	backend, _ := url.Parse("http://127.0.0.1:1")
	handler := newHandler(t, &fakeRunner{err: fmt.Errorf("rollback failed")}, backend)
	shutdown := make(chan struct{}, 1)
	handler.SetShutdown(func() { shutdown <- struct{}{} })

	statusRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/control/status", nil)
	statusRequest.Host = "127.0.0.1:8080"
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/control/actions/stop", nil)
	request.Host = "127.0.0.1:8080"
	request.AddCookie(statusResponse.Result().Cookies()[0])
	request.Header.Set("X-Control-CSRF-Token", handler.csrf)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	select {
	case <-shutdown:
		t.Fatal("failed restoration must keep the panel available")
	default:
	}
}

func TestRoutesFrontendAndReportsOfflineBackend(t *testing.T) {
	t.Parallel()
	backend, _ := url.Parse("http://127.0.0.1:1")
	handler := newHandler(t, &fakeRunner{}, backend)

	page := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/accounts", nil)
	page.Host = "127.0.0.1:8080"
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, page)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), "PTCGP control panel") {
		t.Fatalf("page status=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}

	api := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/bootstrap", nil)
	api.Host = "127.0.0.1:8080"
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, api)
	if apiResponse.Code != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(apiResponse.Result().Body)
		t.Fatalf("api status=%d body=%s", apiResponse.Code, body)
	}
}

func TestRoutesImageAssetsToBackend(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assets/image/example" || r.URL.Query().Get("w") != "120" {
			t.Fatalf("proxied image URL = %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("thumbnail"))
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(t, &fakeRunner{}, backendURL)

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/assets/image/example?w=120", nil)
	request.Host = "127.0.0.1:8080"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || response.Body.String() != "thumbnail" {
		t.Fatalf("status=%d type=%s body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func TestRejectsNonLoopbackHost(t *testing.T) {
	t.Parallel()
	backend, _ := url.Parse("http://127.0.0.1:1")
	handler := newHandler(t, &fakeRunner{}, backend)
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	request.Host = "example.test"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
}
