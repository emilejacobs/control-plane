package devices_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/agentrollout"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
)

// rolloutStore stubs the target-resolution + stamping side.
type rolloutStore struct {
	mu      sync.Mutex
	devices []registry.Device
	setIDs  []string
	setVer  string
}

func (s *rolloutStore) List(context.Context) ([]registry.Device, error) {
	return s.devices, nil
}

func (s *rolloutStore) SetDesiredAgentVersion(_ context.Context, ids []string, version string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setIDs = append([]string(nil), ids...)
	s.setVer = version
	return len(ids), nil
}

// rolloutPusher records PushMany fan-outs.
type rolloutPusher struct {
	mu       sync.Mutex
	pushIDs  []string
	pushVer  string
	pushCorr string
	pushed   int
	err      error
}

func (p *rolloutPusher) PushMany(_ context.Context, ids []string, version, corr string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return 0, p.err
	}
	p.pushIDs = append([]string(nil), ids...)
	p.pushVer, p.pushCorr = version, corr
	if p.pushed == 0 {
		p.pushed = len(ids)
	}
	return p.pushed, nil
}

// rolloutCatalog stubs the release catalog: known versions only.
type rolloutCatalog struct{ known map[string]bool }

func (c *rolloutCatalog) Manifest(_ context.Context, version string) (agentmanifest.Manifest, error) {
	if c.known[version] {
		return agentmanifest.Manifest{Version: version}, nil
	}
	return agentmanifest.Manifest{}, agentrollout.ErrVersionNotFound
}

// auditRecorder captures audit entries.
type auditRecorder struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *auditRecorder) Write(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func sPtr(s string) *string { return &s }

func rolloutFleet() []registry.Device {
	return []registry.Device{
		{ID: "dev-a", IsOnline: true, SiteID: sPtr("site-1")},
		{ID: "dev-b", IsOnline: false, SiteID: sPtr("site-1")},
		{ID: "dev-c", IsOnline: true, SiteID: sPtr("site-2")},
	}
}

func postRollout(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/agent-rollouts", strings.NewReader(body))
	req = req.WithContext(cplog.WithCorrelationID(req.Context(), "corr-roll-1"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Happy path, device_ids selector: every named device is stamped, only the
// online ones are pushed, the set is audited, and the response carries the
// counts + correlation id.
func TestAgentRolloutPostDeviceIDs(t *testing.T) {
	store := &rolloutStore{devices: rolloutFleet()}
	pusher := &rolloutPusher{}
	catalog := &rolloutCatalog{known: map[string]bool{"v1.4.0": true}}
	auditW := &auditRecorder{}
	h := devices.NewAgentRolloutPost(store, catalog, pusher, auditW)

	rec := postRollout(t, h, `{"version":"v1.4.0","device_ids":["dev-a","dev-b"]}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body)
	}

	if got := strings.Join(store.setIDs, ","); got != "dev-a,dev-b" {
		t.Errorf("stamped ids = %q, want dev-a,dev-b", got)
	}
	if store.setVer != "v1.4.0" {
		t.Errorf("stamped version = %q", store.setVer)
	}
	// Only dev-a is online — dev-b converges via reconcile when it reconnects.
	if got := strings.Join(pusher.pushIDs, ","); got != "dev-a" {
		t.Errorf("pushed ids = %q, want dev-a", got)
	}
	if pusher.pushCorr != "corr-roll-1" {
		t.Errorf("push correlation = %q, want corr-roll-1", pusher.pushCorr)
	}

	var resp struct {
		CorrelationID string `json:"correlation_id"`
		Targeted      int    `json:"targeted"`
		Pushed        int    `json:"pushed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response: %v", err)
	}
	if resp.Targeted != 2 || resp.Pushed != 1 || resp.CorrelationID != "corr-roll-1" {
		t.Errorf("response = %+v, want targeted 2 pushed 1 corr-roll-1", resp)
	}

	if len(auditW.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(auditW.entries))
	}
	e := auditW.entries[0]
	if e.Action != "audit.agent_rollout_set" || e.Outcome != "success" {
		t.Errorf("audit = %+v", e)
	}
	if e.ResourceID != "v1.4.0" {
		t.Errorf("audit resource id = %q, want the version", e.ResourceID)
	}
}

// site_id selector targets that site's devices only.
func TestAgentRolloutPostSiteSelector(t *testing.T) {
	store := &rolloutStore{devices: rolloutFleet()}
	pusher := &rolloutPusher{}
	h := devices.NewAgentRolloutPost(store, &rolloutCatalog{known: map[string]bool{"v1.4.0": true}}, pusher, &auditRecorder{})

	rec := postRollout(t, h, `{"version":"v1.4.0","site_id":"site-1"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body)
	}
	if got := strings.Join(store.setIDs, ","); got != "dev-a,dev-b" {
		t.Errorf("stamped ids = %q, want dev-a,dev-b", got)
	}
}

// all:true targets the whole fleet.
func TestAgentRolloutPostAllSelector(t *testing.T) {
	store := &rolloutStore{devices: rolloutFleet()}
	pusher := &rolloutPusher{}
	h := devices.NewAgentRolloutPost(store, &rolloutCatalog{known: map[string]bool{"v1.4.0": true}}, pusher, &auditRecorder{})

	rec := postRollout(t, h, `{"version":"v1.4.0","all":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body)
	}
	if got := strings.Join(store.setIDs, ","); got != "dev-a,dev-b,dev-c" {
		t.Errorf("stamped ids = %q, want the fleet", got)
	}
	if got := strings.Join(pusher.pushIDs, ","); got != "dev-a,dev-c" {
		t.Errorf("pushed ids = %q, want online devices only", got)
	}
}

// A version the catalog doesn't carry is rejected up front — nothing stamped,
// nothing pushed. A rollout can never target a nonexistent version.
func TestAgentRolloutPostUnknownVersion(t *testing.T) {
	store := &rolloutStore{devices: rolloutFleet()}
	pusher := &rolloutPusher{}
	h := devices.NewAgentRolloutPost(store, &rolloutCatalog{known: map[string]bool{}}, pusher, &auditRecorder{})

	rec := postRollout(t, h, `{"version":"v9.9.9","all":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body)
	}
	if len(store.setIDs) != 0 {
		t.Errorf("stamped despite unknown version: %v", store.setIDs)
	}
	if len(pusher.pushIDs) != 0 {
		t.Errorf("pushed despite unknown version: %v", pusher.pushIDs)
	}
}

// Exactly one selector is required.
func TestAgentRolloutPostSelectorValidation(t *testing.T) {
	h := devices.NewAgentRolloutPost(
		&rolloutStore{devices: rolloutFleet()},
		&rolloutCatalog{known: map[string]bool{"v1.4.0": true}},
		&rolloutPusher{}, &auditRecorder{},
	)
	for name, body := range map[string]string{
		"no selector":    `{"version":"v1.4.0"}`,
		"two selectors":  `{"version":"v1.4.0","all":true,"site_id":"site-1"}`,
		"empty version":  `{"device_ids":["dev-a"]}`,
		"empty body":     `{}`,
		"malformed json": `{"version":`,
	} {
		if rec := postRollout(t, h, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

// A target set that matches nothing is a 404 — the operator named unknown
// devices or an empty site.
func TestAgentRolloutPostNoTargets(t *testing.T) {
	h := devices.NewAgentRolloutPost(
		&rolloutStore{devices: rolloutFleet()},
		&rolloutCatalog{known: map[string]bool{"v1.4.0": true}},
		&rolloutPusher{}, &auditRecorder{},
	)
	rec := postRollout(t, h, `{"version":"v1.4.0","device_ids":["ghost-1"]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body)
	}
}
