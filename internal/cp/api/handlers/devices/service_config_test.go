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

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
)

// configStore stubs the persistence side of the PUT handler. Captures
// the (allowList, interval) tuple it was called with so the test can
// assert nil-vs-empty was preserved through the API → registry hop.
type configStore struct {
	mu     sync.Mutex
	calls  []configCall
	known  map[string]bool // deviceID → exists
	setErr error
}

type configCall struct {
	deviceID  string
	allowList *[]string
	interval  *string
}

func (s *configStore) GetByID(_ context.Context, id string) (registry.Device, error) {
	if s.known[id] {
		return registry.Device{ID: id}, nil
	}
	return registry.Device{}, registry.ErrDeviceNotFound
}
func (s *configStore) SetServiceConfig(_ context.Context, deviceID string, allowList *[]string, interval *string) error {
	s.mu.Lock()
	s.calls = append(s.calls, configCall{deviceID: deviceID, allowList: allowList, interval: interval})
	s.mu.Unlock()
	return s.setErr
}

// cmdPublisher captures every Publish call so the test can verify the
// config.update envelope shape + topic.
type cmdPublisher struct {
	mu     sync.Mutex
	calls  []pubCall
	pubErr error
}
type pubCall struct {
	topic   string
	payload []byte
}

func (p *cmdPublisher) Publish(_ context.Context, topic string, payload []byte) error {
	p.mu.Lock()
	p.calls = append(p.calls, pubCall{topic: topic, payload: append([]byte(nil), payload...)})
	p.mu.Unlock()
	return p.pubErr
}

// Happy path: PUT persists the override, publishes a config.update on
// devices/{id}/cmd carrying the override fields, and returns 202 with
// the correlation_id so the dashboard can poll for the agent's ACK.
func TestServiceConfigPutHappyPath(t *testing.T) {
	store := &configStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{}
	h := devices.NewServiceConfigPut(store, pub)

	body := `{"service_allow_list":["nginx","anydesk"],"service_status_interval":"2m"}`
	req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/service-config", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	req = req.WithContext(cplog.WithCorrelationID(req.Context(), "corr-test-42"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	if len(store.calls) != 1 {
		t.Fatalf("SetServiceConfig calls: got %d, want 1", len(store.calls))
	}
	c := store.calls[0]
	if c.deviceID != "dev-abc" {
		t.Errorf("deviceID: got %q", c.deviceID)
	}
	if c.allowList == nil || (*c.allowList)[0] != "nginx" || (*c.allowList)[1] != "anydesk" {
		t.Errorf("allowList: got %v", c.allowList)
	}
	if c.interval == nil || *c.interval != "2m" {
		t.Errorf("interval: got %v", c.interval)
	}

	if len(pub.calls) != 1 {
		t.Fatalf("Publish calls: got %d, want 1", len(pub.calls))
	}
	pcall := pub.calls[0]
	if pcall.topic != "devices/dev-abc/cmd" {
		t.Errorf("topic: got %q, want devices/dev-abc/cmd", pcall.topic)
	}
	var cmd envelope.Command
	if err := json.Unmarshal(pcall.payload, &cmd); err != nil {
		t.Fatalf("publish payload not a valid Command: %v", err)
	}
	if cmd.Type != "config.update" {
		t.Errorf("cmd type: got %q, want config.update", cmd.Type)
	}
	if cmd.CorrelationID != "corr-test-42" {
		t.Errorf("correlation_id: got %q, want corr-test-42", cmd.CorrelationID)
	}
	if cmd.CommandID == "" {
		t.Error("command_id is empty; expected a freshly minted value")
	}
	// Cmd args echo the payload fields.
	var args struct {
		AllowList json.RawMessage `json:"service_allow_list"`
		Interval  json.RawMessage `json:"service_status_interval"`
	}
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		t.Fatalf("cmd.Args: %v", err)
	}
	if !bytes.Equal(args.AllowList, []byte(`["nginx","anydesk"]`)) {
		t.Errorf("cmd args allow_list: got %s", args.AllowList)
	}
	if !bytes.Equal(args.Interval, []byte(`"2m"`)) {
		t.Errorf("cmd args interval: got %s", args.Interval)
	}

	// Response body returns correlation_id so the dashboard can poll
	// the device-detail endpoint until last_applied_correlation_id
	// matches.
	var resp struct {
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CorrelationID != "corr-test-42" {
		t.Errorf("response correlation_id: got %q", resp.CorrelationID)
	}
}

// Missing device → 404, no Publish, no Set.
func TestServiceConfigPut404OnUnknownDevice(t *testing.T) {
	store := &configStore{known: map[string]bool{}}
	pub := &cmdPublisher{}
	h := devices.NewServiceConfigPut(store, pub)

	req := httptest.NewRequest(http.MethodPut, "/devices/dev-nope/service-config", strings.NewReader(`{"service_allow_list":[]}`))
	req.SetPathValue("id", "dev-nope")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 0 {
		t.Errorf("Set should not be called on 404; got %d", len(store.calls))
	}
	if len(pub.calls) != 0 {
		t.Errorf("Publish should not be called on 404; got %d", len(pub.calls))
	}
}

// Validation mirrors the agent-side handler (ADR-028 whitelist): bad
// interval / bad service name / unknown field return 400 with a JSON
// error envelope. Neither Set nor Publish runs.
func TestServiceConfigPutValidates(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"bad interval too short", `{"service_status_interval":"5s"}`},
		{"bad interval unparseable", `{"service_status_interval":"oops"}`},
		{"bad service name empty", `{"service_allow_list":[""]}`},
		{"unknown field", `{"broker_url":"evil"}`},
		{"malformed JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &configStore{known: map[string]bool{"dev-abc": true}}
			pub := &cmdPublisher{}
			h := devices.NewServiceConfigPut(store, pub)
			req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/service-config", strings.NewReader(tc.body))
			req.SetPathValue("id", "dev-abc")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if len(store.calls) != 0 {
				t.Errorf("Set should not be called on 400; got %d", len(store.calls))
			}
			if len(pub.calls) != 0 {
				t.Errorf("Publish should not be called on 400; got %d", len(pub.calls))
			}
		})
	}
}

// Publish failure AFTER a successful Set: the override is persisted but
// the agent never gets the push. The handler must surface this as 502
// (the cp-side bookkeeping is correct but the downstream channel
// failed) — operator can retry; cp-side is idempotent on repeat.
func TestServiceConfigPutBadGatewayOnPublishFailure(t *testing.T) {
	store := &configStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{pubErr: errors.New("iot data plane unreachable")}
	h := devices.NewServiceConfigPut(store, pub)

	req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/service-config", strings.NewReader(`{"service_allow_list":["nginx"]}`))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 1 {
		t.Errorf("Set should still have run before publish failure; got %d", len(store.calls))
	}
}
