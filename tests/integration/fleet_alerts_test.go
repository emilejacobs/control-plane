package integration_test

import (
	"context"
	"testing"
	"time"

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
