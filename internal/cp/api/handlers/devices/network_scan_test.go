package devices_test

import (
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
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
)

// networkScanStore stubs the NetworkScanStore the POST + GET handlers
// depend on. Mirrors logTailStore from log_tail_test.go.
type networkScanStore struct {
	mu        sync.Mutex
	created   []registry.NetworkScanRequest
	known     map[string]bool // deviceID → exists
	getRet    map[string]registry.NetworkScan
	getErr    error
	createErr error
}

func (s *networkScanStore) GetByID(_ context.Context, id string) (registry.Device, error) {
	if s.known[id] {
		return registry.Device{ID: id}, nil
	}
	return registry.Device{}, registry.ErrDeviceNotFound
}
func (s *networkScanStore) CreateNetworkScanRequest(_ context.Context, req registry.NetworkScanRequest) error {
	s.mu.Lock()
	s.created = append(s.created, req)
	s.mu.Unlock()
	return s.createErr
}
func (s *networkScanStore) GetNetworkScan(_ context.Context, corrID string) (registry.NetworkScan, error) {
	if s.getErr != nil {
		return registry.NetworkScan{}, s.getErr
	}
	n, ok := s.getRet[corrID]
	if !ok {
		return registry.NetworkScan{}, registry.ErrNetworkScanNotFound
	}
	return n, nil
}

// === POST /devices/{id}/network-scan ===

// Happy path: POST persists a pending row, publishes network.scan on
// devices/{id}/cmd, returns 202 + correlation_id.
func TestNetworkScanPostHappyPath(t *testing.T) {
	store := &networkScanStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{}
	h := devices.NewNetworkScanPost(store, pub)

	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/network-scan",
		strings.NewReader(`{"cidr":"192.168.1.0/24"}`))
	req.SetPathValue("id", "dev-abc")
	req = req.WithContext(cplog.WithCorrelationID(req.Context(), "corr-ns-1"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("CreateNetworkScanRequest calls: got %d, want 1", len(store.created))
	}
	c := store.created[0]
	if c.CorrelationID != "corr-ns-1" || c.DeviceID != "dev-abc" || c.CIDR != "192.168.1.0/24" {
		t.Errorf("create args: got %+v", c)
	}
	if len(pub.calls) != 1 || pub.calls[0].topic != "devices/dev-abc/cmd" {
		t.Fatalf("publish: got %+v", pub.calls)
	}
	var cmd envelope.Command
	_ = json.Unmarshal(pub.calls[0].payload, &cmd)
	if cmd.Type != "network.scan" || cmd.CorrelationID != "corr-ns-1" {
		t.Errorf("cmd envelope: got %+v", cmd)
	}
	var body struct {
		CorrelationID string `json:"correlation_id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.CorrelationID != "corr-ns-1" {
		t.Errorf("response correlation_id: got %q", body.CorrelationID)
	}
}

// Empty payload = auto-detect mode. CIDR is empty in both the
// persisted row and the cmd payload.
func TestNetworkScanPostAutoDetect(t *testing.T) {
	store := &networkScanStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{}
	h := devices.NewNetworkScanPost(store, pub)

	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/network-scan",
		strings.NewReader(`{}`))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 || store.created[0].CIDR != "" {
		t.Errorf("create args: got %+v", store.created)
	}
}

// 404 on unknown device — no create, no publish.
func TestNetworkScanPost404OnUnknownDevice(t *testing.T) {
	store := &networkScanStore{known: map[string]bool{}}
	pub := &cmdPublisher{}
	h := devices.NewNetworkScanPost(store, pub)
	req := httptest.NewRequest(http.MethodPost, "/devices/missing/network-scan",
		strings.NewReader(`{}`))
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
func TestNetworkScanPostValidates(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"bad cidr", `{"cidr":"not-a-cidr"}`},
		{"unknown field", `{"cidr":"10.0.0.0/24","extra":"x"}`},
		{"malformed JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &networkScanStore{known: map[string]bool{"dev-abc": true}}
			pub := &cmdPublisher{}
			h := devices.NewNetworkScanPost(store, pub)
			req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/network-scan",
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

// 502 on publish failure after a successful create — the pending row
// persists; operator retries.
func TestNetworkScanPostBadGatewayOnPublishFailure(t *testing.T) {
	store := &networkScanStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{pubErr: errors.New("iot unreachable")}
	h := devices.NewNetworkScanPost(store, pub)
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/network-scan",
		strings.NewReader(`{}`))
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

// === GET /devices/{id}/network-scan/{correlation_id} ===

func TestNetworkScanGetPending(t *testing.T) {
	requestedAt := time.Date(2026, 5, 26, 14, 30, 0, 0, time.UTC)
	cidr := "192.168.1.0/24"
	store := &networkScanStore{
		known: map[string]bool{"dev-abc": true},
		getRet: map[string]registry.NetworkScan{
			"corr-1": {
				CorrelationID: "corr-1", DeviceID: "dev-abc",
				CIDR: &cidr, Status: "pending", RequestedAt: requestedAt,
			},
		},
	}
	h := devices.NewNetworkScanGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/network-scan/corr-1", nil)
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
	if body["result"] != nil {
		t.Errorf("result: got %v, want nil while pending", body["result"])
	}
	if body["cidr"] != "192.168.1.0/24" {
		t.Errorf("cidr: got %v, want 192.168.1.0/24", body["cidr"])
	}
}

func TestNetworkScanGetDone(t *testing.T) {
	requestedAt := time.Date(2026, 5, 26, 14, 30, 0, 0, time.UTC)
	returnedAt := requestedAt.Add(3 * time.Second)
	result := networkscan.Response{
		Hosts: []networkscan.Host{
			{IP: "192.168.1.10", MAC: "44:19:b6:aa:bb:cc", Vendor: "Hikvision", OpenPorts: []int{80, 554}},
		},
	}
	store := &networkScanStore{
		known: map[string]bool{"dev-abc": true},
		getRet: map[string]registry.NetworkScan{
			"corr-2": {
				CorrelationID: "corr-2", DeviceID: "dev-abc",
				Status: "done", Result: &result,
				RequestedAt: requestedAt, ReturnedAt: &returnedAt,
			},
		},
	}
	h := devices.NewNetworkScanGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/network-scan/corr-2", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("correlation_id", "corr-2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Result *struct {
			Hosts []struct {
				IP        string `json:"ip"`
				Vendor    string `json:"vendor"`
				OpenPorts []int  `json:"open_ports"`
			} `json:"hosts"`
		} `json:"result"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "done" {
		t.Errorf("status: got %q, want done", body.Status)
	}
	if body.Result == nil || len(body.Result.Hosts) != 1 || body.Result.Hosts[0].Vendor != "Hikvision" {
		t.Errorf("result: got %+v", body.Result)
	}
}

func TestNetworkScanGet404OnUnknownCorrelation(t *testing.T) {
	store := &networkScanStore{known: map[string]bool{"dev-abc": true}, getRet: map[string]registry.NetworkScan{}}
	h := devices.NewNetworkScanGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/network-scan/nope", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("correlation_id", "nope")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestNetworkScanGet404OnUnknownDevice(t *testing.T) {
	store := &networkScanStore{known: map[string]bool{}}
	h := devices.NewNetworkScanGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/missing/network-scan/anything", nil)
	req.SetPathValue("id", "missing")
	req.SetPathValue("correlation_id", "anything")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// Defence in depth: an out-of-scope correlation_id on the wrong device
// returns 404, not the row from a sibling device. Mirrors log-tail's
// device-id mismatch guard.
func TestNetworkScanGet404OnDeviceMismatch(t *testing.T) {
	store := &networkScanStore{
		known: map[string]bool{"dev-abc": true, "dev-xyz": true},
		getRet: map[string]registry.NetworkScan{
			"corr-xyz": {CorrelationID: "corr-xyz", DeviceID: "dev-xyz", Status: "done",
				RequestedAt: time.Now().UTC()},
		},
	}
	h := devices.NewNetworkScanGet(store)
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/network-scan/corr-xyz", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("correlation_id", "corr-xyz")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}
