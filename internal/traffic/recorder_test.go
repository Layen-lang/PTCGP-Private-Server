package traffic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRecorderRedactsSecretsAndRestoresRecentEvents(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "traffic.jsonl")
	recorder, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	recorder.Record(Entry{
		Protocol:  "grpc",
		Method:    "/System/AuthorizeV1",
		Status:    "OK",
		StartedAt: time.Now(),
		Request:   &api.SystemAuthorizeV1_Types_Request{DeviceAccount: "private-device"},
		Response:  &api.SystemAuthorizeV1_Types_Response{SessionToken: "private-session", PlayerId: "player-1"},
	})
	values := recorder.Recent(10)
	if len(values) != 1 {
		t.Fatalf("recent count=%d, want 1", len(values))
	}
	encoded, err := json.Marshal(values[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private-device")) || bytes.Contains(encoded, []byte("private-session")) {
		t.Fatalf("event contains credentials: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte("[REDACTED]")) || !bytes.Contains(encoded, []byte("player-1")) {
		t.Fatalf("event does not preserve safe diagnostics: %s", encoded)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := restored.Recent(10); len(got) != 1 || got[0].Method != "/System/AuthorizeV1" {
		t.Fatalf("restored events=%+v", got)
	}
}

func TestHTTPMiddlewarePreservesBodiesAndCapturesResponse(t *testing.T) {
	t.Parallel()
	recorder, err := Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	handler := recorder.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	}))
	request := httptest.NewRequest(http.MethodPost, "https://local.test/example", bytes.NewBufferString(`{"password":"hidden","value":7}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Body.String() != `{"password":"hidden","value":7}` {
		t.Fatalf("response status=%d body=%s", response.Code, response.Body.String())
	}
	values := recorder.Recent(1)
	if len(values) != 1 || values[0].Status != "201" || bytes.Contains(values[0].Request.Data, []byte("hidden")) {
		t.Fatalf("captured event=%+v", values)
	}
}

func TestInvalidJSONNeverLeaksItsRawBody(t *testing.T) {
	t.Parallel()
	recorder, err := Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	recorder.Record(Entry{
		Protocol:           "http",
		Method:             "POST /login",
		Status:             "400",
		Request:            []byte(`{"password":"leaked"`),
		RequestContentType: "application/json",
	})
	values := recorder.Recent(1)
	if len(values) != 1 || bytes.Contains(values[0].Request.Data, []byte("leaked")) || !bytes.Contains(values[0].Request.Data, []byte("INVALID JSON REDACTED")) {
		t.Fatalf("invalid JSON payload=%s", values[0].Request.Data)
	}
}

func TestUnaryInterceptorCapturesDecodedMessages(t *testing.T) {
	t.Parallel()
	recorder, err := Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	interceptor := recorder.UnaryServerInterceptor(func(context.Context) string { return "player-1" })
	request := &api.EchoEchoV1_Types_Request{}
	response, err := interceptor(context.Background(), request, &grpc.UnaryServerInfo{FullMethod: "/Echo/EchoV1"}, func(context.Context, any) (any, error) {
		return &api.EchoEchoV1_Types_Response{Time: timestamppb.Now()}, nil
	})
	if err != nil || response == nil {
		t.Fatalf("response=%v err=%v", response, err)
	}
	values := recorder.Recent(1)
	if len(values) != 1 || values[0].PlayerID != "player-1" || values[0].Status != "OK" {
		t.Fatalf("captured event=%+v", values)
	}
}

func TestAfterReturnsOnlyNewerEvents(t *testing.T) {
	t.Parallel()
	recorder, err := Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	recorder.Record(Entry{Protocol: "grpc", Method: "/System/LoginV1", Status: "OK"})
	first := recorder.Recent(1)[0]
	recorder.Record(Entry{Protocol: "grpc", Method: "/PlayerResources/SyncV1", Status: "OK"})
	newer := recorder.After(first.ID, 10)
	if len(newer) != 1 || newer[0].Method != "/PlayerResources/SyncV1" {
		t.Fatalf("newer events=%+v", newer)
	}
	if got := recorder.After("unknown-event", 1); len(got) != 1 || got[0].Method != "/PlayerResources/SyncV1" {
		t.Fatalf("fallback events=%+v", got)
	}
}

func TestSubscribeSignalsWithoutBlockingRecord(t *testing.T) {
	t.Parallel()
	recorder, err := Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	updates, cancel := recorder.Subscribe()
	defer cancel()

	recorder.Record(Entry{Protocol: "grpc", Method: "/System/LoginV1", Status: "OK"})
	recorder.Record(Entry{Protocol: "grpc", Method: "/PlayerResources/SyncV1", Status: "OK"})
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("traffic subscriber was not notified")
	}
	if got := recorder.Recent(2); len(got) != 2 {
		t.Fatalf("recording was blocked, events=%+v", got)
	}
}

func TestSlowSubscriberDoesNotSerializeConcurrentRequests(t *testing.T) {
	t.Parallel()
	recorder, err := Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	_, cancel := recorder.Subscribe()
	defer cancel()

	const workers = 16
	const eventsPerWorker = 20
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer group.Done()
			for event := 0; event < eventsPerWorker; event++ {
				recorder.Record(Entry{Protocol: "grpc", Method: "/Concurrent/Request", Status: "OK"})
			}
		}(worker)
	}
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent recording was blocked by a slow subscriber")
	}
	if got := len(recorder.Recent(workers * eventsPerWorker)); got != workers*eventsPerWorker {
		t.Fatalf("recorded events=%d, want %d", got, workers*eventsPerWorker)
	}
}
