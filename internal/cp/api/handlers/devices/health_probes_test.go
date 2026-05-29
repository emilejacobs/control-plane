package devices_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type fakeHealthProbeStore struct {
	device    registry.Device
	deviceErr error
	probes    []registry.DeviceHealthProbe
}

func (s fakeHealthProbeStore) GetByID(_ context.Context, _ string) (registry.Device, error) {
	return s.device, s.deviceErr
}

func (s fakeHealthProbeStore) ListHealthProbes(_ context.Context, _ string) ([]registry.DeviceHealthProbe, error) {
	return s.probes, nil
}

func doProbeGet(t *testing.T, h http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/devices/"+id+"/health-probes", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthProbeListReturnsProbes(t *testing.T) {
	observedAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	store := fakeHealthProbeStore{
		probes: []registry.DeviceHealthProbe{
			{Name: "auto_login", Status: "green", State: "configured", LastObservedAt: observedAt},
			{Name: "whisper_model", Status: "green", State: "present", Details: map[string]any{"variant": "medium.en"}, LastObservedAt: observedAt},
		},
	}
	rec := doProbeGet(t, devices.NewHealthProbeList(store), "dev-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Probes []struct {
			Name           string         `json:"name"`
			Status         string         `json:"status"`
			State          string         `json:"state"`
			Details        map[string]any `json:"details"`
			LastObservedAt string         `json:"last_observed_at"`
		} `json:"probes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Probes) != 2 {
		t.Fatalf("probes len = %d, want 2", len(body.Probes))
	}
	if body.Probes[0].Name != "auto_login" || body.Probes[0].Status != "green" || body.Probes[0].State != "configured" {
		t.Errorf("probe[0] = %+v", body.Probes[0])
	}
	if body.Probes[0].LastObservedAt != observedAt.Format(time.RFC3339) {
		t.Errorf("last_observed_at = %q, want RFC3339 %q", body.Probes[0].LastObservedAt, observedAt.Format(time.RFC3339))
	}
	if body.Probes[1].Details["variant"] != "medium.en" {
		t.Errorf("probe[1] details lost: %+v", body.Probes[1].Details)
	}
}

func TestHealthProbeListEmptyReturnsArrayNotNull(t *testing.T) {
	rec := doProbeGet(t, devices.NewHealthProbeList(fakeHealthProbeStore{probes: nil}), "dev-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got == "" || !contains(got, `"probes":[]`) {
		t.Errorf("body = %q, want probes:[] (not null)", got)
	}
}

func TestHealthProbeListUnknownDevice404(t *testing.T) {
	store := fakeHealthProbeStore{deviceErr: registry.ErrDeviceNotFound}
	rec := doProbeGet(t, devices.NewHealthProbeList(store), "nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
