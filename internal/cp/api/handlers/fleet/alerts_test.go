package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type fakeAlertStore struct {
	alerts registry.FleetAlerts
	err    error
}

func (f *fakeAlertStore) FleetAlerts(context.Context) (registry.FleetAlerts, error) {
	return f.alerts, f.err
}

// TestAlertsHandlerEnvelope — the handler serializes the registry roll-up
// into the {probes, services} envelope with device-id lists inline, and the
// red/yellow/stopped lists are always arrays (never null) so the UI can map
// over them unconditionally.
func TestAlertsHandlerEnvelope(t *testing.T) {
	store := &fakeAlertStore{alerts: registry.FleetAlerts{
		Probes: []registry.ProbeAlert{
			{ProbeName: "plate_recognizer_container", Red: []string{"dev-a"}, Yellow: []string{"dev-b"}},
		},
		Services: []registry.ServiceAlert{
			{ServiceName: "usb_audio", Stopped: []string{"dev-a"}},
		},
	}}

	rec := httptest.NewRecorder()
	NewAlerts(store).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fleet/alerts", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Probes []struct {
			ProbeName string   `json:"probe_name"`
			Red       []string `json:"red"`
			Yellow    []string `json:"yellow"`
		} `json:"probes"`
		Services []struct {
			ServiceName string   `json:"service_name"`
			Stopped     []string `json:"stopped"`
		} `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Probes) != 1 || body.Probes[0].ProbeName != "plate_recognizer_container" {
		t.Fatalf("probes = %+v", body.Probes)
	}
	if len(body.Probes[0].Red) != 1 || body.Probes[0].Red[0] != "dev-a" {
		t.Errorf("red = %v", body.Probes[0].Red)
	}
	if len(body.Probes[0].Yellow) != 1 || body.Probes[0].Yellow[0] != "dev-b" {
		t.Errorf("yellow = %v", body.Probes[0].Yellow)
	}
	if len(body.Services) != 1 || body.Services[0].ServiceName != "usb_audio" {
		t.Fatalf("services = %+v", body.Services)
	}
	if len(body.Services[0].Stopped) != 1 || body.Services[0].Stopped[0] != "dev-a" {
		t.Errorf("stopped = %v", body.Services[0].Stopped)
	}

	// Raw-JSON guard: empty groups serialize as [] not null.
	empty := &fakeAlertStore{alerts: registry.FleetAlerts{}}
	rec2 := httptest.NewRecorder()
	NewAlerts(empty).ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/fleet/alerts", nil))
	if got := rec2.Body.String(); !json.Valid(rec2.Body.Bytes()) ||
		!containsBoth(got, `"probes":[]`, `"services":[]`) {
		t.Errorf("empty roll-up body = %s, want [] arrays not null", got)
	}
}

func containsBoth(s, a, b string) bool {
	return contains(s, a) && contains(s, b)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
