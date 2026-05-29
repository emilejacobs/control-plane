package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

// TestRegistryFleetAlertsGroupsByType — the #21 fleet-alerts roll-up groups
// affected devices by probe_name (red / yellow) and by service_name
// (stopped), and is alert-only: probes that are green and services that are
// running contribute no entry at all.
func TestRegistryFleetAlertsGroupsByType(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	devA := enrollForTest(t, srv, "mac-alerts-a", "aaaaaaaa-0000-0000-0000-000000000001")
	devB := enrollForTest(t, srv, "mac-alerts-b", "bbbbbbbb-0000-0000-0000-000000000002")

	observedAt := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	// devA: plate_recognizer_container RED, auto_login GREEN (omitted).
	if err := srv.Registry.RecordHealthProbes(ctx, devA, []healthprobes.Result{
		{Name: "plate_recognizer_container", Status: healthprobes.StatusRed, State: "exited"},
		{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
	}, observedAt); err != nil {
		t.Fatalf("record probes devA: %v", err)
	}
	// devB: plate_recognizer_container YELLOW (same probe, warn tier).
	if err := srv.Registry.RecordHealthProbes(ctx, devB, []healthprobes.Result{
		{Name: "plate_recognizer_container", Status: healthprobes.StatusYellow, State: "restarting"},
	}, observedAt); err != nil {
		t.Fatalf("record probes devB: %v", err)
	}
	// devA: usb_audio service stopped; whisper running (omitted).
	if err := srv.Registry.RecordServiceStates(ctx, devA, []servicestatus.ServiceState{
		{Name: "usb_audio", State: service.StateStopped, StateSince: observedAt},
		{Name: "whisper", State: service.StateRunning, StateSince: observedAt},
	}, observedAt); err != nil {
		t.Fatalf("record services devA: %v", err)
	}

	alerts, err := srv.Registry.FleetAlerts(staffCtx(ctx))
	if err != nil {
		t.Fatalf("FleetAlerts: %v", err)
	}

	if len(alerts.Probes) != 1 {
		t.Fatalf("probe groups = %d, want 1 (only plate_recognizer_container; green omitted)", len(alerts.Probes))
	}
	p := alerts.Probes[0]
	if p.ProbeName != "plate_recognizer_container" {
		t.Errorf("probe name = %q", p.ProbeName)
	}
	if len(p.Red) != 1 || p.Red[0] != devA {
		t.Errorf("red = %v, want [%s]", p.Red, devA)
	}
	if len(p.Yellow) != 1 || p.Yellow[0] != devB {
		t.Errorf("yellow = %v, want [%s]", p.Yellow, devB)
	}

	if len(alerts.Services) != 1 {
		t.Fatalf("service groups = %d, want 1 (only usb_audio; running omitted)", len(alerts.Services))
	}
	s := alerts.Services[0]
	if s.ServiceName != "usb_audio" {
		t.Errorf("service name = %q", s.ServiceName)
	}
	if len(s.Stopped) != 1 || s.Stopped[0] != devA {
		t.Errorf("stopped = %v, want [%s]", s.Stopped, devA)
	}
}

// TestRegistryFleetAlertsSiteScoped — a non-staff operator's roll-up only
// includes devices at their allowlisted sites: a red probe on a device at a
// denied site never surfaces. Guards the security-critical scoping path.
func TestRegistryFleetAlertsSiteScoped(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	siteAllowed := insertSite(t, ctx, srv, clientID, "Allowed Site")
	siteDenied := insertSite(t, ctx, srv, clientID, "Denied Site")
	devAllowed := insertDeviceAtSite(t, ctx, srv, "mac-allowed", siteAllowed)
	devDenied := insertDeviceAtSite(t, ctx, srv, "mac-denied", siteDenied)

	observedAt := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	for _, dev := range []string{devAllowed, devDenied} {
		if err := srv.Registry.RecordHealthProbes(ctx, dev, []healthprobes.Result{
			{Name: "plate_recognizer_container", Status: healthprobes.StatusRed, State: "exited"},
		}, observedAt); err != nil {
			t.Fatalf("record probes %s: %v", dev, err)
		}
	}

	scoped := authz.ContextWithScope(ctx, authz.SiteFilter{SiteIDs: []string{siteAllowed}})
	alerts, err := srv.Registry.FleetAlerts(scoped)
	if err != nil {
		t.Fatalf("FleetAlerts: %v", err)
	}

	if len(alerts.Probes) != 1 {
		t.Fatalf("probe groups = %d, want 1", len(alerts.Probes))
	}
	if got := alerts.Probes[0].Red; len(got) != 1 || got[0] != devAllowed {
		t.Errorf("red = %v, want only allowed-site device [%s] (denied %s leaked?)", got, devAllowed, devDenied)
	}
}

// TestRegistryFleetAlertsNoScopeFailsClosed — a read with no resolved scope
// returns an empty roll-up rather than the whole fleet.
func TestRegistryFleetAlertsNoScopeFailsClosed(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	dev := enrollForTest(t, srv, "mac-noscope", "cccccccc-0000-0000-0000-000000000003")
	if err := srv.Registry.RecordHealthProbes(ctx, dev, []healthprobes.Result{
		{Name: "plate_recognizer_container", Status: healthprobes.StatusRed, State: "exited"},
	}, time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record probes: %v", err)
	}

	alerts, err := srv.Registry.FleetAlerts(ctx) // no ContextWithScope
	if err != nil {
		t.Fatalf("FleetAlerts: %v", err)
	}
	if len(alerts.Probes) != 0 || len(alerts.Services) != 0 {
		t.Errorf("unscoped read returned %d probe / %d service groups, want 0/0 (fail closed)",
			len(alerts.Probes), len(alerts.Services))
	}
}

// TestFleetAlertsEndpoint — GET /fleet/alerts round-trips the roll-up
// through the real router + auth + scope + DB under a {probes, services}
// envelope with device ids inline.
func TestFleetAlertsEndpoint(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	dev := enrollForTest(t, srv, "mac-alerts-api", "dddddddd-0000-0000-0000-000000000004")
	observedAt := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	if err := srv.Registry.RecordHealthProbes(ctx, dev, []healthprobes.Result{
		{Name: "plate_recognizer_container", Status: healthprobes.StatusRed, State: "exited"},
	}, observedAt); err != nil {
		t.Fatalf("record probes: %v", err)
	}
	if err := srv.Registry.RecordServiceStates(ctx, dev, []servicestatus.ServiceState{
		{Name: "usb_audio", State: service.StateStopped, StateSince: observedAt},
	}, observedAt); err != nil {
		t.Fatalf("record services: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/fleet/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+mintAccessToken(t, ctx, srv))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /fleet/alerts: status %d; body=%s", resp.StatusCode, raw)
	}

	var body struct {
		Probes []struct {
			ProbeName string   `json:"probe_name"`
			Red       []string `json:"red"`
		} `json:"probes"`
		Services []struct {
			ServiceName string   `json:"service_name"`
			Stopped     []string `json:"stopped"`
		} `json:"services"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Probes) != 1 || body.Probes[0].ProbeName != "plate_recognizer_container" ||
		len(body.Probes[0].Red) != 1 || body.Probes[0].Red[0] != dev {
		t.Errorf("probes = %+v, want one red plate_recognizer_container with [%s]", body.Probes, dev)
	}
	if len(body.Services) != 1 || body.Services[0].ServiceName != "usb_audio" ||
		len(body.Services[0].Stopped) != 1 || body.Services[0].Stopped[0] != dev {
		t.Errorf("services = %+v, want one stopped usb_audio with [%s]", body.Services, dev)
	}
}
