package telemetry_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
	"github.com/emilejacobs/control-plane/internal/telemetry"
)

// stubProbeBackend returns a fixed result set, standing in for the
// OS-specific probes.Backend.
type stubProbeBackend struct {
	results []healthprobes.Result
}

func (s stubProbeBackend) Collect(_ context.Context) []healthprobes.Result { return s.results }

func TestProbeCollectorStampsReport(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	backend := stubProbeBackend{results: []healthprobes.Result{
		{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
		{Name: healthprobes.ProbeGUISession, Status: healthprobes.StatusRed, State: "login_window"},
	}}
	c := &telemetry.ProbeCollector{
		Backend:  backend,
		DeviceID: "dev-1234",
		Now:      func() time.Time { return now },
	}

	report := c.Collect(context.Background())

	if report.DeviceID != "dev-1234" {
		t.Errorf("DeviceID = %q, want dev-1234", report.DeviceID)
	}
	if report.CorrelationID == "" {
		t.Error("CorrelationID is empty, want a stamped id")
	}
	if !report.ReportedAt.Equal(now) {
		t.Errorf("ReportedAt = %v, want %v", report.ReportedAt, now)
	}
	if len(report.Probes) != 2 || report.Probes[0].Name != healthprobes.ProbeAutoLogin {
		t.Errorf("Probes not carried through: %+v", report.Probes)
	}
}

func TestProbePublisherEmitsOneTick(t *testing.T) {
	tr := newRecordingTransport()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	backend := stubProbeBackend{results: []healthprobes.Result{
		{Name: healthprobes.ProbeUSBAudio, Status: healthprobes.StatusRed, State: "missing"},
	}}
	c := &telemetry.ProbeCollector{Backend: backend, DeviceID: "dev-1234", Now: func() time.Time { return now }}

	p := &telemetry.ProbePublisher{
		Interval:  5 * time.Millisecond,
		DeviceID:  "dev-1234",
		Collect:   c.Collect,
		Transport: tr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	select {
	case <-tr.gotOne:
	case <-time.After(time.Second):
		t.Fatal("no publish within 1s")
	}
	cancel()
	<-done

	publishes := tr.snapshot("devices/dev-1234/health-probes")
	if len(publishes) == 0 {
		t.Fatalf("expected a publish on devices/dev-1234/health-probes; got topics: %v", topicsIn(tr))
	}
	var got healthprobes.Report
	if err := json.Unmarshal(publishes[0], &got); err != nil {
		t.Fatalf("payload not valid Report JSON: %v\nraw: %s", err, publishes[0])
	}
	if got.DeviceID != "dev-1234" || len(got.Probes) != 1 || got.Probes[0].Name != healthprobes.ProbeUSBAudio {
		t.Errorf("payload round-trip lost data: %+v", got)
	}
}
