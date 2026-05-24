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
