package devices

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// fakeService is a stand-in for the registry. It returns one preset device
// (or error) so handler tests stay free of Postgres.
type fakeService struct {
	dev           registry.Device
	devs          []registry.Device
	err           error
	serviceConfig registry.ServiceConfig
	serviceCfgErr error
}

func (f fakeService) GetByID(_ context.Context, _ string) (registry.Device, error) {
	return f.dev, f.err
}

func (f fakeService) List(_ context.Context) ([]registry.Device, error) {
	return f.devs, f.err
}

func (f fakeService) ListServices(_ context.Context, _ string) ([]registry.DeviceService, error) {
	return nil, nil
}

func (f fakeService) GetServiceConfig(_ context.Context, _ string) (registry.ServiceConfig, error) {
	return f.serviceConfig, f.serviceCfgErr
}

// getDevice drives GetHandler.ServeHTTP and returns the decoded JSON body.
func getDevice(t *testing.T, h *GetHandler) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-1", nil)
	req.SetPathValue("id", "dev-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// Phase 2 Chain A: the fleet LIST endpoint must surface cert expiry +
// agent_version per device so the overview tiles (Cert expiring ≤ 30d,
// Agent version drift, Cert expiring soonest rollup) can compute their
// summaries client-side without an extra fetch per device.
func TestListDevicesSurfacesCertAndAgentVersion(t *testing.T) {
	now := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	expiry1 := now.Add(20 * 24 * time.Hour) // 20 days
	expiry2 := now.Add(180 * 24 * time.Hour)
	h := NewList(fakeService{devs: []registry.Device{
		{ID: "dev-a", Hostname: "mac-a", AgentVersion: "0.1.0", MtlsCertExpiresAt: &expiry1},
		{ID: "dev-b", Hostname: "mac-b", AgentVersion: "0.2.0", MtlsCertExpiresAt: &expiry2},
		{ID: "dev-c", Hostname: "mac-c", AgentVersion: "0.1.0", MtlsCertExpiresAt: nil}, // pre-cert-tracking device
	}})
	// Inject a deterministic clock for the days-remaining math.
	h.now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Devices) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(body.Devices))
	}

	// Index by hostname for stable assertions.
	byHost := map[string]map[string]any{}
	for _, d := range body.Devices {
		byHost[d["hostname"].(string)] = d
	}

	// dev-a: 20 days remaining + agent_version present.
	if got := byHost["mac-a"]["mtls_cert_days_remaining"]; got != float64(20) {
		t.Errorf("mac-a cert_days_remaining: got %v, want 20", got)
	}
	if got := byHost["mac-a"]["agent_version"]; got != "0.1.0" {
		t.Errorf("mac-a agent_version: got %v, want 0.1.0", got)
	}
	if got := byHost["mac-a"]["mtls_cert_expires_at"]; got == nil {
		t.Errorf("mac-a cert_expires_at should not be null")
	}

	// dev-b: 180 days remaining, different agent_version.
	if got := byHost["mac-b"]["mtls_cert_days_remaining"]; got != float64(180) {
		t.Errorf("mac-b cert_days_remaining: got %v, want 180", got)
	}
	if got := byHost["mac-b"]["agent_version"]; got != "0.2.0" {
		t.Errorf("mac-b agent_version: got %v, want 0.2.0", got)
	}

	// dev-c: cert fields null (pre-tracking), but agent_version present.
	if got, ok := byHost["mac-c"]["mtls_cert_days_remaining"]; !ok || got != nil {
		t.Errorf("mac-c cert_days_remaining: got %v, want null", got)
	}
	if got, ok := byHost["mac-c"]["mtls_cert_expires_at"]; !ok || got != nil {
		t.Errorf("mac-c cert_expires_at: got %v, want null", got)
	}
}

func TestGetDeviceComputesCertDaysRemaining(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(30 * 24 * time.Hour)
	h := NewGet(fakeService{dev: registry.Device{
		ID:                "dev-1",
		EnrolledAt:        now,
		MtlsCertExpiresAt: &expiry,
	}})
	h.now = func() time.Time { return now }

	out := getDevice(t, h)

	// JSON numbers decode into map[string]any as float64.
	got, ok := out["mtls_cert_days_remaining"].(float64)
	if !ok {
		t.Fatalf("mtls_cert_days_remaining: got %v (%T) want a number", out["mtls_cert_days_remaining"], out["mtls_cert_days_remaining"])
	}
	if want := 30; int(got) != want {
		t.Errorf("mtls_cert_days_remaining: got %d want %d", int(got), want)
	}
}

func TestGetDeviceExpiredCertHasNegativeDaysRemaining(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(-5 * 24 * time.Hour) // expired 5 days ago
	h := NewGet(fakeService{dev: registry.Device{
		ID:                "dev-1",
		EnrolledAt:        now.Add(-365 * 24 * time.Hour),
		MtlsCertExpiresAt: &expiry,
	}})
	h.now = func() time.Time { return now }

	out := getDevice(t, h)

	got, ok := out["mtls_cert_days_remaining"].(float64)
	if !ok {
		t.Fatalf("mtls_cert_days_remaining: got %v (%T) want a number", out["mtls_cert_days_remaining"], out["mtls_cert_days_remaining"])
	}
	if want := -5; int(got) != want {
		t.Errorf("mtls_cert_days_remaining: got %d want %d", int(got), want)
	}
}

func TestGetDeviceReturnsCertExpiresAt(t *testing.T) {
	expiry := time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)
	h := NewGet(fakeService{dev: registry.Device{
		ID:                "dev-1",
		Hostname:          "mac-mini-acme-01",
		EnrolledAt:        time.Now(),
		MtlsCertExpiresAt: &expiry,
	}})

	out := getDevice(t, h)

	got, ok := out["mtls_cert_expires_at"].(string)
	if !ok {
		t.Fatalf("mtls_cert_expires_at: got %v (%T) want a string", out["mtls_cert_expires_at"], out["mtls_cert_expires_at"])
	}
	if want := "2027-01-15T12:00:00Z"; got != want {
		t.Errorf("mtls_cert_expires_at: got %q want %q", got, want)
	}
}

func TestGetDeviceSurfacesSiteAndClient(t *testing.T) {
	site := "Acme HQ"
	client := "Acme Corp"
	h := NewGet(fakeService{dev: registry.Device{
		ID:         "dev-1",
		EnrolledAt: time.Now(),
		SiteName:   &site,
		ClientName: &client,
	}})

	out := getDevice(t, h)

	if got := out["site_name"]; got != site {
		t.Errorf("site_name: got %v want %q", got, site)
	}
	if got := out["client_name"]; got != client {
		t.Errorf("client_name: got %v want %q", got, client)
	}
}

func TestGetDeviceSiteAndClientAreNullWhenUnassigned(t *testing.T) {
	h := NewGet(fakeService{dev: registry.Device{
		ID:         "dev-1",
		EnrolledAt: time.Now(),
	}})

	out := getDevice(t, h)

	if got, ok := out["site_name"]; !ok || got != nil {
		t.Errorf("site_name: got %v want null", got)
	}
	if got, ok := out["client_name"]; !ok || got != nil {
		t.Errorf("client_name: got %v want null", got)
	}
}

// Phase 2 slice 2: service_config block renders override + last-applied
// from the registry's ServiceConfig. No override ⇒ both *override fields
// null; no ACK yet ⇒ last_applied_* null. Dashboard differentiates
// (default) vs (overridden) from allow_list_override !== null.
func TestGetDeviceServiceConfigNoOverride(t *testing.T) {
	h := NewGet(fakeService{
		dev:           registry.Device{ID: "dev-1", EnrolledAt: time.Now()},
		serviceConfig: registry.ServiceConfig{},
	})

	out := getDevice(t, h)

	cfg, ok := out["service_config"].(map[string]any)
	if !ok {
		t.Fatalf("service_config missing or wrong type: %T", out["service_config"])
	}
	if got, ok := cfg["allow_list_override"]; !ok || got != nil {
		t.Errorf("allow_list_override: got %v want null", got)
	}
	if got, ok := cfg["interval_override"]; !ok || got != nil {
		t.Errorf("interval_override: got %v want null", got)
	}
	if got, ok := cfg["last_applied_at"]; !ok || got != nil {
		t.Errorf("last_applied_at: got %v want null", got)
	}
	if got, ok := cfg["last_applied_correlation_id"]; !ok || got != nil {
		t.Errorf("last_applied_correlation_id: got %v want null", got)
	}
}

func TestGetDeviceServiceConfigWithOverride(t *testing.T) {
	list := []string{"com.uknomi.webui", "anydesk"}
	interval := "2m"
	at := time.Date(2026, 5, 24, 19, 0, 0, 0, time.UTC)
	corr := "corr-aaa"
	h := NewGet(fakeService{
		dev: registry.Device{ID: "dev-1", EnrolledAt: time.Now()},
		serviceConfig: registry.ServiceConfig{
			AllowListOverride:        &list,
			IntervalOverride:         &interval,
			LastAppliedAt:            &at,
			LastAppliedCorrelationID: &corr,
		},
	})

	out := getDevice(t, h)
	cfg, ok := out["service_config"].(map[string]any)
	if !ok {
		t.Fatalf("service_config missing: %T", out["service_config"])
	}
	gotList, ok := cfg["allow_list_override"].([]any)
	if !ok || len(gotList) != 2 || gotList[0] != "com.uknomi.webui" {
		t.Errorf("allow_list_override: got %v", cfg["allow_list_override"])
	}
	if got := cfg["interval_override"]; got != "2m" {
		t.Errorf("interval_override: got %v want 2m", got)
	}
	if got := cfg["last_applied_at"]; got != "2026-05-24T19:00:00Z" {
		t.Errorf("last_applied_at: got %v", got)
	}
	if got := cfg["last_applied_correlation_id"]; got != "corr-aaa" {
		t.Errorf("last_applied_correlation_id: got %v", got)
	}
}
