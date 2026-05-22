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
	dev registry.Device
	err error
}

func (f fakeService) GetByID(_ context.Context, _ string) (registry.Device, error) {
	return f.dev, f.err
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
