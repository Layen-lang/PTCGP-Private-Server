// Package traffic records sanitized game requests and responses for local
// diagnostics without exposing credentials or session tokens.
package traffic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	defaultRecentLimit  = 500
	defaultPayloadLimit = 512 << 10
	defaultFileLimit    = 64 << 20
)

// Payload is a sanitized representation of one request or response body.
type Payload struct {
	Encoding  string          `json:"encoding"`
	SizeBytes int             `json:"sizeBytes"`
	Truncated bool            `json:"truncated,omitempty"`
	Data      json.RawMessage `json:"data"`
}

// Event represents one completed exchange between the game and the server.
type Event struct {
	ID         string    `json:"id"`
	CapturedAt time.Time `json:"capturedAt"`
	Protocol   string    `json:"protocol"`
	Method     string    `json:"method"`
	PlayerID   string    `json:"playerId,omitempty"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"durationMs"`
	Error      string    `json:"error,omitempty"`
	Request    Payload   `json:"request"`
	Response   Payload   `json:"response"`
}

// Entry contains the values needed to record a completed exchange.
type Entry struct {
	Protocol            string
	Method              string
	PlayerID            string
	Status              string
	Error               string
	StartedAt           time.Time
	Request             any
	Response            any
	RequestContentType  string
	ResponseContentType string
}

// Recorder keeps a bounded in-memory timeline and appends the same sanitized
// events to a JSONL file for later analysis.
type Recorder struct {
	mu            sync.RWMutex
	subscribersMu sync.RWMutex
	file          *os.File
	path          string
	recent        []Event
	subscribers   map[uint64]chan struct{}
	recentLimit   int
	payloadLimit  int
	fileLimit     int64
	lastWriteErr  error
	sequence      atomic.Uint64
	subscriberID  atomic.Uint64
}

// Open creates a recorder and restores the most recent events from the current
// JSONL file. The caller must close the recorder during server shutdown.
func Open(path string) (*Recorder, error) {
	if path == "" {
		return nil, errors.New("traffic log path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create traffic log directory: %w", err)
	}
	recorder := &Recorder{
		path:         path,
		recentLimit:  defaultRecentLimit,
		payloadLimit: defaultPayloadLimit,
		fileLimit:    defaultFileLimit,
	}
	if err := recorder.loadRecent(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open traffic log: %w", err)
	}
	recorder.file = file
	return recorder, nil
}

// ReadHistory loads a read-only snapshot without opening a second log writer.
func ReadHistory(path string) (*Recorder, error) {
	r := &Recorder{path: path, recentLimit: defaultRecentLimit}
	if err := r.loadRecent(); err != nil {
		return nil, err
	}
	return r, nil
}

// Close flushes the operating-system file handle used by the recorder.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// Record sanitizes and persists an exchange. Recording failures never affect
// the game request; malformed values are represented as diagnostic text.
func (r *Recorder) Record(entry Entry) {
	if r == nil {
		return
	}
	capturedAt := time.Now().UTC()
	startedAt := entry.StartedAt
	if startedAt.IsZero() {
		startedAt = capturedAt
	}
	event := Event{
		ID:         fmt.Sprintf("%d-%06d", capturedAt.UnixNano(), r.sequence.Add(1)),
		CapturedAt: capturedAt,
		Protocol:   entry.Protocol,
		Method:     entry.Method,
		PlayerID:   entry.PlayerID,
		Status:     entry.Status,
		DurationMS: time.Since(startedAt).Milliseconds(),
		Error:      entry.Error,
		Request:    r.payload(entry.Request, entry.RequestContentType),
		Response:   r.payload(entry.Response, entry.ResponseContentType),
	}
	line, err := json.Marshal(event)
	if err != nil {
		return
	}
	line = append(line, '\n')

	func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.recent = append(r.recent, event)
		if overflow := len(r.recent) - r.recentLimit; overflow > 0 {
			copy(r.recent, r.recent[overflow:])
			r.recent = r.recent[:r.recentLimit]
		}
		if r.file == nil {
			return
		}
		if info, statErr := r.file.Stat(); statErr == nil && info.Size()+int64(len(line)) > r.fileLimit {
			if rotateErr := r.rotateLocked(); rotateErr != nil {
				return
			}
		}
		if _, err := r.file.Write(line); err != nil {
			r.lastWriteErr = err
		} else {
			r.lastWriteErr = nil
		}
	}()
	r.notifySubscribers()
}

// Subscribe reports that one or more new events are available. Notifications
// are coalesced so a slow diagnostics client can never delay game requests;
// subscribers recover every event from After using their last event ID.
func (r *Recorder) Subscribe() (<-chan struct{}, func()) {
	updates := make(chan struct{}, 1)
	if r == nil {
		close(updates)
		return updates, func() {}
	}
	id := r.subscriberID.Add(1)
	r.subscribersMu.Lock()
	if r.subscribers == nil {
		r.subscribers = make(map[uint64]chan struct{})
	}
	r.subscribers[id] = updates
	r.subscribersMu.Unlock()

	var once sync.Once
	return updates, func() {
		once.Do(func() {
			r.subscribersMu.Lock()
			if current, ok := r.subscribers[id]; ok {
				delete(r.subscribers, id)
				close(current)
			}
			r.subscribersMu.Unlock()
		})
	}
}

func (r *Recorder) notifySubscribers() {
	r.subscribersMu.RLock()
	defer r.subscribersMu.RUnlock()
	for _, updates := range r.subscribers {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
}

// Recent returns newest-first copies of the latest recorded exchanges.
func (r *Recorder) Recent(limit int) []Event {
	if r == nil || limit <= 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit > len(r.recent) {
		limit = len(r.recent)
	}
	return cloneNewest(r.recent, limit)
}

// After returns newest-first events recorded after the supplied event ID. An
// unknown ID falls back to the latest events so a restarted client recovers.
func (r *Recorder) After(id string, limit int) []Event {
	if r == nil || limit <= 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	position := -1
	for index := len(r.recent) - 1; index >= 0; index-- {
		if r.recent[index].ID == id {
			position = index
			break
		}
	}
	if position < 0 {
		return cloneNewest(r.recent, limit)
	}
	return cloneNewest(r.recent[position+1:], limit)
}

// HTTPMiddleware records REST requests without changing their body or response.
func (r *Recorder) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestBytes, body := readAndRestore(request.Body, r.payloadLimit+1)
		request.Body = body
		writer := &captureResponseWriter{ResponseWriter: w, status: http.StatusOK, body: newLimitedBuffer(r.payloadLimit + 1)}
		next.ServeHTTP(writer, request)
		r.Record(Entry{
			Protocol:            "http",
			Method:              request.Method + " " + request.URL.Path,
			Status:              fmt.Sprintf("%d", writer.status),
			StartedAt:           startedAt,
			Request:             requestBytes,
			Response:            writer.body.Bytes(),
			RequestContentType:  request.Header.Get("Content-Type"),
			ResponseContentType: writer.Header().Get("Content-Type"),
		})
	})
}

// UnaryServerInterceptor records decoded Protobuf requests and responses. The
// optional playerID callback reads an authenticated local player from context.
func (r *Recorder) UnaryServerInterceptor(playerID func(context.Context) string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		startedAt := time.Now()
		response, err := handler(ctx, request)
		id := ""
		if playerID != nil {
			id = playerID(ctx)
		}
		r.Record(Entry{
			Protocol:  "grpc",
			Method:    info.FullMethod,
			PlayerID:  id,
			Status:    status.Code(err).String(),
			Error:     errorText(err),
			StartedAt: startedAt,
			Request:   request,
			Response:  response,
		})
		return response, err
	}
}

// StreamServerInterceptor records the first decoded message in each direction.
// This also captures descriptor-compatibility calls served as unknown streams.
func (r *Recorder) StreamServerInterceptor(playerID func(context.Context) string) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		startedAt := time.Now()
		captured := &captureServerStream{ServerStream: stream}
		err := handler(server, captured)
		id := ""
		if playerID != nil {
			id = playerID(stream.Context())
		}
		r.Record(Entry{
			Protocol:  "grpc-stream",
			Method:    info.FullMethod,
			PlayerID:  id,
			Status:    status.Code(err).String(),
			Error:     errorText(err),
			StartedAt: startedAt,
			Request:   captured.request,
			Response:  captured.response,
		})
		return err
	}
}

func (r *Recorder) payload(value any, contentType string) Payload {
	raw, encoding := marshalValue(value, contentType)
	size := len(raw)
	if encoding == "json" {
		raw = sanitizeJSON(raw)
	}
	if len(raw) > r.payloadLimit {
		preview := raw[:r.payloadLimit]
		return Payload{Encoding: encoding, SizeBytes: size, Truncated: true, Data: mustJSON(string(preview))}
	}
	if len(raw) == 0 {
		return Payload{Encoding: "empty", Data: json.RawMessage("null")}
	}
	if encoding == "json" && json.Valid(raw) {
		return Payload{Encoding: encoding, SizeBytes: size, Data: json.RawMessage(bytes.Clone(raw))}
	}
	return Payload{Encoding: encoding, SizeBytes: size, Data: mustJSON(string(raw))}
}

func marshalValue(value any, contentType string) ([]byte, string) {
	if value == nil {
		return nil, "empty"
	}
	if message, ok := value.(proto.Message); ok {
		data, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
		if err != nil {
			return []byte(err.Error()), "text"
		}
		return data, "json"
	}
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = bytes.Clone(typed)
	case json.RawMessage:
		data = bytes.Clone(typed)
	case string:
		data = []byte(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return []byte(err.Error()), "text"
		}
		data = encoded
	}
	if json.Valid(data) || strings.Contains(strings.ToLower(contentType), "json") {
		return data, "json"
	}
	if utf8.Valid(data) {
		return data, "text"
	}
	return []byte(base64.StdEncoding.EncodeToString(data)), "base64"
}

func sanitizeJSON(data []byte) []byte {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return mustJSON("[INVALID JSON REDACTED]")
	}
	redact(value)
	result, err := json.Marshal(value)
	if err != nil {
		return data
	}
	return result
}

func redact(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveKey(key) {
				typed[key] = "[REDACTED]"
				continue
			}
			redact(child)
		}
	case []any:
		for _, child := range typed {
			redact(child)
		}
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	for _, fragment := range []string{
		"sessiontoken", "sessionid", "accesstoken", "refreshtoken", "idtoken",
		"authorization", "credential", "password", "secret", "deviceaccount",
		"federationtoken", "jwttoken", "authorizationcode", "cookie",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func (r *Recorder) loadRecent() error {
	file, err := os.Open(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read traffic log: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		r.recent = append(r.recent, event)
		if overflow := len(r.recent) - r.recentLimit; overflow > 0 {
			copy(r.recent, r.recent[overflow:])
			r.recent = r.recent[:r.recentLimit]
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan traffic log: %w", err)
	}
	return nil
}

func (r *Recorder) rotateLocked() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	r.file = nil
	archive := r.path + ".1"
	if err := os.Remove(archive); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, r.reopenLocked())
	}
	if err := os.Rename(r.path, archive); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, r.reopenLocked())
	}
	return r.reopenLocked()
}

func (r *Recorder) reopenLocked() error {
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	r.file = file
	return nil
}

func readAndRestore(body io.ReadCloser, limit int) ([]byte, io.ReadCloser) {
	if body == nil {
		return nil, http.NoBody
	}
	data, err := io.ReadAll(io.LimitReader(body, int64(limit)))
	restored := struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(data), body), Closer: body}
	if err != nil {
		return []byte(err.Error()), restored
	}
	return data, restored
}

func cloneEvent(event Event) Event {
	event.Request.Data = bytes.Clone(event.Request.Data)
	event.Response.Data = bytes.Clone(event.Response.Data)
	return event
}

func cloneNewest(events []Event, limit int) []Event {
	if limit > len(events) {
		limit = len(events)
	}
	result := make([]Event, limit)
	for index := 0; index < limit; index++ {
		result[index] = cloneEvent(events[len(events)-1-index])
	}
	return result
}

type captureResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        *limitedBuffer
}

func (w *captureResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.body.Write(data)
	return w.ResponseWriter.Write(data)
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func newLimitedBuffer(limit int) *limitedBuffer { return &limitedBuffer{limit: limit} }

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.Buffer.Write(data)
	}
	return originalLength, nil
}

type captureServerStream struct {
	grpc.ServerStream
	request  any
	response any
}

func (s *captureServerStream) RecvMsg(message any) error {
	err := s.ServerStream.RecvMsg(message)
	if err == nil && s.request == nil {
		s.request = cloneMessage(message)
	}
	return err
}

func (s *captureServerStream) SendMsg(message any) error {
	if s.response == nil {
		s.response = cloneMessage(message)
	}
	return s.ServerStream.SendMsg(message)
}

func cloneMessage(value any) any {
	if message, ok := value.(proto.Message); ok {
		return proto.Clone(message)
	}
	return value
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func mustJSON(value string) json.RawMessage {
	return json.RawMessage(strconv.Quote(value))
}
