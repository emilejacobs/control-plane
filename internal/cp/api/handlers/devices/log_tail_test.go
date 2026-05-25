package devices_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
)

// logTailStore stubs the LogTailStore the POST + GET handlers depend on.
type logTailStore struct {
	mu       sync.Mutex
	created  []registry.LogTailRequest
	known    map[string]bool // deviceID → exists
	getRet   map[string]registry.LogTail
	getErr   error
	createErr error
}

func (s *logTailStore) GetByID(_ context.Context, id string) (registry.Device, error) {
	if s.known[id] {
		return registry.Device{ID: id}, nil
	}
	return registry.Device{}, registry.ErrDeviceNotFound
}
func (s *logTailStore) CreateLogTailRequest(_ context.Context, req registry.LogTailRequest) error {
	s.mu.Lock()
	s.created = append(s.created, req)
	s.mu.Unlock()
	return s.createErr
}
func (s *logTailStore) GetLogTail(_ context.Context, corrID string) (registry.LogTail, error) {
	if s.getErr != nil {
		return registry.LogTail{}, s.getErr
	}
	t, ok := s.getRet[corrID]
	if !ok {
		return registry.LogTail{}, registry.ErrLogTailNotFound
	}
	return t, nil
}

// === POST /devices/{id}/logs/tail ===

// Happy path: POST persists the pending row, publishes log.tail on
// devices/{id}/cmd, returns 202 + correlation_id.
func TestLogTailPostHappyPath(t *testing.T) {
	store := &logTailStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{}
	h := devices.NewLogTailPost(store, pub)

	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/logs/tail",
		strings.NewReader(`{"log_name":"agent","lines":200}`))
	req.SetPathValue("id", "dev-abc")
	req = req.WithContext(cplog.WithCorrelationID(req.Context(), "corr-lt-7"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}

	if len(store.created) != 1 {
		t.Fatalf("CreateLogTailRequest calls: got %d, want 1", len(store.created))
	}
	c := store.created[0]
	if c.CorrelationID != "corr-lt-7" || c.DeviceID != "dev-abc" || c.LogName != "agent" || c.LinesRequested != 200 {
		t.Errorf("create args: got %+v", c)
	}

	if len(pub.calls) != 1 || pub.calls[0].topic != "devices/dev-abc/cmd" {
		t.Fatalf("publish: got %+v", pub.calls)
	}
	var cmd envelope.Command
	_ = json.Unmarshal(pub.calls[0].payload, &cmd)
	if cmd.Type != "log.tail" || cmd.CorrelationID != "corr-lt-7" {
		t.Errorf("cmd envelope: got %+v", cmd)
	}
	if !bytes.Contains(cmd.Args, []byte(`"log_name":"agent"`)) || !bytes.Contains(cmd.Args, []byte(`"lines":200`)) {
		t.Errorf("cmd args: got %s", cmd.Args)
	}

	var body struct {
		CorrelationID string `json:"correlation_id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.CorrelationID != "corr-lt-7" {
		t.Errorf("response correlation_id: got %q", body.CorrelationID)
	}
}

// 404 on unknown device — no create, no publish.
func TestLogTailPost404OnUnknownDevice(t *testing.T) {
	store := &logTailStore{known: map[string]bool{}}
	pub := &cmdPublisher{}
	h := devices.NewLogTailPost(store, pub)
	req := httptest.NewRequest(http.MethodPost, "/devices/missing/logs/tail",
		strings.NewReader(`{"log_name":"agent","lines":100}`))
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	if len(store.created) != 0 || len(pub.calls) != 0 {
		t.Error("create/publish should not run on 404")
	}
}

// 400 on bad payload — no create, no publish.
func TestLogTailPostValidates(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"bad lines", `{"log_name":"agent","lines":1000}`},
		{"bad log_name empty", `{"log_name":"","lines":100}`},
		{"unknown field", `{"log_name":"agent","lines":100,"path":"/etc/passwd"}`},
		{"malformed JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &logTailStore{known: map[string]bool{"dev-abc": true}}
			pub := &cmdPublisher{}
			h := devices.NewLogTailPost(store, pub)
			req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/logs/tail",
				strings.NewReader(tc.body))
			req.SetPathValue("id", "dev-abc")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if len(store.created) != 0 || len(pub.calls) != 0 {
				t.Error("create/publish should not run on 400")
			}
		})
	}
}

// 502 on publish failure AFTER successful create. Operator retries
// the same Idempotency-Key; the registry write is idempotent.
func TestLogTailPostBadGatewayOnPublishFailure(t *testing.T) {
	store := &logTailStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{pubErr: errors.New("iot unreachable")}
	h := devices.NewLogTailPost(store, pub)
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/logs/tail",
		strings.NewReader(`{"log_name":"agent","lines":50}`))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", rec.Code)
	}
	if len(store.created) != 1 {
		t.Error("create should still have run before publish failure")
	}
}

// === GET /devices/{id}/logs/tail/{correlation_id} ===

func TestLogTailGetPending(t *testing.T) {
	requestedAt := time.Date(2026, 5, 24, 22, 0, 0, 0, time.UTC)
	store := &logTailStore{
		known: map[string]bool{"dev-abc": true},
		getRet: map[string]registry.LogTail{
			"corr-1": {
				CorrelationID: "corr-1", DeviceID: "dev-abc",
				LogName: "agent", LinesRequested: 100,
				Status: "pending", RequestedAt: requestedAt,
			},
		},
	}
	h := devices.NewLogTailGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/logs/tail/corr-1", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("correlation_id", "corr-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["status"] != "pending" {
		t.Errorf("status: got %v", body["status"])
	}
	if body["content"] != nil {
		t.Errorf("content: got %v, want nil while pending", body["content"])
	}
}

func TestLogTailGetDone(t *testing.T) {
	requestedAt := time.Date(2026, 5, 24, 22, 0, 0, 0, time.UTC)
	returnedAt := requestedAt.Add(2 * time.Second)
	content := "line 1\nline 2\n"
	store := &logTailStore{
		known: map[string]bool{"dev-abc": true},
		getRet: map[string]registry.LogTail{
			"corr-2": {
				CorrelationID: "corr-2", DeviceID: "dev-abc",
				LogName: "agent", LinesRequested: 100,
				Status: "done", Content: &content,
				RequestedAt: requestedAt, ReturnedAt: &returnedAt,
			},
		},
	}
	h := devices.NewLogTailGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/logs/tail/corr-2", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("correlation_id", "corr-2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["status"] != "done" || body["content"] != content {
		t.Errorf("got %+v", body)
	}
}

func TestLogTailGet404OnUnknownCorrelation(t *testing.T) {
	store := &logTailStore{known: map[string]bool{"dev-abc": true}, getRet: map[string]registry.LogTail{}}
	h := devices.NewLogTailGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/logs/tail/nope", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("correlation_id", "nope")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestLogTailGet404OnUnknownDevice(t *testing.T) {
	store := &logTailStore{known: map[string]bool{}}
	h := devices.NewLogTailGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/missing/logs/tail/anything", nil)
	req.SetPathValue("id", "missing")
	req.SetPathValue("correlation_id", "anything")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}
