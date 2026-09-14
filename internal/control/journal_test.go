package control

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalPersistsRotatesAndBoundsHistory(t *testing.T) {
	j, err := NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		j.Record("local", "info", fmt.Sprint(i))
	}
	reopened, err := NewJournal(filepath.Dir(j.path))
	if err != nil {
		t.Fatal(err)
	}
	events, err := reopened.Recent()
	if err != nil || len(events) != 1000 || events[0].Message != "200" || events[999].Time.IsZero() {
		t.Fatalf("persisted history: count=%d error=%v", len(events), err)
	}
	j.Record("local", "info", strings.Repeat("x", 4<<20))
	j.Record("stop", "success", "stopped")
	if _, err := os.Stat(j.path + ".1"); err != nil {
		t.Fatal(err)
	}
	events, err = j.Recent()
	if err != nil || events[len(events)-1].Message != "stopped" {
		t.Fatalf("rotation: %v", err)
	}
}

func TestAdministrationAndLogsAvailableDuringOperation(t *testing.T) {
	backend, _ := url.Parse("http://127.0.0.1:1")
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	h := newHandler(t, runner, backend)
	j, err := NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.SetAdministration(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "accounts available") }), j, "package", "activity")
	done := make(chan struct{})
	go func() { h.action(httptest.NewRecorder(), nil, "local"); close(done) }()
	<-runner.started
	defer func() { close(runner.release); <-done }()
	for _, path := range []string{"/api/bootstrap", "/api/control/logs", "/api/control/logs/export", "/logs"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.Code)
		}
		if path == "/api/control/logs" && !strings.Contains(response.Body.String(), `"operation":"local"`) {
			t.Fatal("missing live operation")
		}
		if path == "/api/control/logs/export" && (!strings.Contains(response.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(response.Body.String(), "[local]")) {
			t.Fatal("export must download the live operation as text")
		}
	}
}

func TestActionFailureIsPersisted(t *testing.T) {
	backend, _ := url.Parse("http://127.0.0.1:1")
	h := newHandler(t, &fakeRunner{err: fmt.Errorf("device disconnected")}, backend)
	j, err := NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.journal = j
	response := httptest.NewRecorder()
	h.action(response, nil, "local")
	events, err := j.Recent()
	if err != nil || len(events) != 2 || events[1].Level != "error" || response.Code != 500 || h.currentOperation() != "" {
		t.Fatalf("failed action: events=%v error=%v", events, err)
	}
}

func TestNativeProgressIsImmediatelyPersisted(t *testing.T) {
	j, err := NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &NativeRunner{}
	runner.SetJournal(j)
	runner.step("local", "preparing server")
	events, err := j.Recent()
	if err != nil || len(events) != 1 || events[0].Message != "preparing server" {
		t.Fatalf("progress=%v error=%v", events, err)
	}
}
