package fleet_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/fleet"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type rolloutDeviceStore struct{ devices []registry.Device }

func (s *rolloutDeviceStore) List(context.Context) ([]registry.Device, error) {
	return s.devices, nil
}

func ver(s string) *string { return &s }

// GET /fleet/agent-rollout derives rollout state from desired-vs-reported
// per device (ADR-035 §4 — no campaign entity): done = reported matches
// desired, in_flight = targeted but drifted, untargeted = no desired set.
func TestAgentRolloutViewDerivesState(t *testing.T) {
	store := &rolloutDeviceStore{devices: []registry.Device{
		{ID: "dev-a", Hostname: "mac-01", AgentVersion: "v1.5.0", DesiredAgentVersion: ver("v1.5.0"), IsOnline: true},
		{ID: "dev-b", Hostname: "mac-02", AgentVersion: "v1.4.0", DesiredAgentVersion: ver("v1.5.0"), IsOnline: false},
		{ID: "dev-c", Hostname: "mac-03", AgentVersion: "v1.4.0", IsOnline: true},
	}}
	h := fleet.NewAgentRollout(store)

	req := httptest.NewRequest(http.MethodGet, "/fleet/agent-rollout", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Counts struct {
			Done       int `json:"done"`
			InFlight   int `json:"in_flight"`
			Untargeted int `json:"untargeted"`
		} `json:"counts"`
		Devices []struct {
			ID              string  `json:"id"`
			Hostname        string  `json:"hostname"`
			ReportedVersion string  `json:"reported_version"`
			DesiredVersion  *string `json:"desired_version"`
			IsOnline        bool    `json:"is_online"`
			State           string  `json:"state"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response: %v", err)
	}
	if resp.Counts.Done != 1 || resp.Counts.InFlight != 1 || resp.Counts.Untargeted != 1 {
		t.Errorf("counts = %+v, want 1/1/1", resp.Counts)
	}
	if len(resp.Devices) != 3 {
		t.Fatalf("devices = %d, want 3", len(resp.Devices))
	}
	wantStates := map[string]string{"dev-a": "done", "dev-b": "in_flight", "dev-c": "untargeted"}
	for _, d := range resp.Devices {
		if d.State != wantStates[d.ID] {
			t.Errorf("device %s state = %q, want %q", d.ID, d.State, wantStates[d.ID])
		}
	}
	// The drilldown needs the raw pair, not just the derived state.
	if resp.Devices[1].ReportedVersion != "v1.4.0" || resp.Devices[1].DesiredVersion == nil || *resp.Devices[1].DesiredVersion != "v1.5.0" {
		t.Errorf("device drilldown fields = %+v", resp.Devices[1])
	}
}

// An empty (or fully out-of-scope) fleet renders zero counts and an empty
// device list, not nulls.
func TestAgentRolloutViewEmptyFleet(t *testing.T) {
	h := fleet.NewAgentRollout(&rolloutDeviceStore{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fleet/agent-rollout", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if body == "" || body[0] != '{' {
		t.Fatalf("body: %s", body)
	}
	var resp map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if string(resp["devices"]) == "null" {
		t.Error("devices is null, want []")
	}
}
