package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/service"
	"github.com/emilejacobs/control-plane/internal/telemetry"
)

func TestServiceStatusCollectorTracerBullet(t *testing.T) {
	now := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	backend := &service.Fake{States: map[string]service.State{
		"com.uknomi.edge-ui": service.StateRunning,
		"nginx":              service.StateRunning,
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-bbe0540c",
		AllowList: []string{"com.uknomi.edge-ui", "nginx"},
		Now:       func() time.Time { return now },
	}

	report := c.Collect(context.Background())

	if report.DeviceID != "dev-bbe0540c" {
		t.Errorf("DeviceID: got %q, want %q", report.DeviceID, "dev-bbe0540c")
	}
	if report.CorrelationID == "" {
		t.Error("CorrelationID is empty; expected a non-empty value")
	}
	if !report.ReportedAt.Equal(now) {
		t.Errorf("ReportedAt: got %v, want %v", report.ReportedAt, now)
	}
	if len(report.Services) != 2 {
		t.Fatalf("Services: got %d entries, want 2", len(report.Services))
	}

	byName := map[string]telemetry.ServiceState{}
	for _, s := range report.Services {
		byName[s.Name] = s
	}
	for _, name := range []string{"com.uknomi.edge-ui", "nginx"} {
		s, ok := byName[name]
		if !ok {
			t.Errorf("missing service entry: %q", name)
			continue
		}
		if s.State != service.StateRunning {
			t.Errorf("%s State: got %q, want %q", name, s.State, service.StateRunning)
		}
	}
}

// A service the backend doesn't know about must still appear in the
// report so the dashboard can show the gap (rather than silently
// dropping the entry). State = "unknown" is the contract; cp-ingest
// persists it as-is and the alarm wiring treats unknown as not-failed.
func TestServiceStatusCollectorReportsUnknownForNotFound(t *testing.T) {
	now := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	backend := &service.Fake{States: map[string]service.State{
		"com.uknomi.edge-ui": service.StateRunning,
		// "nginx" deliberately absent → Fake returns ErrNotFound
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-test",
		AllowList: []string{"com.uknomi.edge-ui", "nginx"},
		Now:       func() time.Time { return now },
	}

	report := c.Collect(context.Background())

	byName := map[string]telemetry.ServiceState{}
	for _, s := range report.Services {
		byName[s.Name] = s
	}

	if got := byName["com.uknomi.edge-ui"].State; got != service.StateRunning {
		t.Errorf("edge-ui State: got %q, want %q", got, service.StateRunning)
	}
	if got := byName["nginx"].State; got != service.StateUnknown {
		t.Errorf("nginx State: got %q, want %q", got, service.StateUnknown)
	}
}

// When the observed state hasn't changed between two Collect calls,
// StateSince must remain at the original observation time. The
// dashboard renders "running since N hours" off this value; if it
// reset every tick, every service would always read "running since 5 min".
func TestServiceStatusCollectorStateSinceStableAcrossCalls(t *testing.T) {
	first := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)

	tick := first
	backend := &service.Fake{States: map[string]service.State{
		"nginx": service.StateRunning,
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-test",
		AllowList: []string{"nginx"},
		Now:       func() time.Time { return tick },
	}

	r1 := c.Collect(context.Background())
	tick = second
	r2 := c.Collect(context.Background())

	if got := r1.Services[0].StateSince; !got.Equal(first) {
		t.Fatalf("r1 StateSince: got %v, want %v", got, first)
	}
	if got := r2.Services[0].StateSince; !got.Equal(first) {
		t.Errorf("r2 StateSince should still be the original observation time; got %v, want %v", got, first)
	}
}

// When the observed state changes between two Collect calls, StateSince
// must advance to the second call's time. This is the converse of the
// stability test — together they pin the "since" semantics.
func TestServiceStatusCollectorStateSinceUpdatesOnTransition(t *testing.T) {
	first := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)

	tick := first
	backend := &service.Fake{States: map[string]service.State{
		"nginx": service.StateRunning,
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-test",
		AllowList: []string{"nginx"},
		Now:       func() time.Time { return tick },
	}

	_ = c.Collect(context.Background())

	// Service transitions Running → Stopped between calls.
	backend.States["nginx"] = service.StateStopped
	tick = second

	r2 := c.Collect(context.Background())

	if got := r2.Services[0].State; got != service.StateStopped {
		t.Fatalf("r2 State: got %q, want %q", got, service.StateStopped)
	}
	if got := r2.Services[0].StateSince; !got.Equal(second) {
		t.Errorf("r2 StateSince should advance to the transition time; got %v, want %v", got, second)
	}
}
