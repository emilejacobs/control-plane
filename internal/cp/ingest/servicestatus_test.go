package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

type serviceStatusWriteCall struct {
	deviceID   string
	states     []servicestatus.ServiceState
	reportedAt time.Time
}

// fakeServiceStatusWriter records every RecordServiceStates call and
// returns its configured error. Mirrors heartbeat_test.go's fakeWriter.
type fakeServiceStatusWriter struct {
	err   error
	calls []serviceStatusWriteCall
}

func (f *fakeServiceStatusWriter) RecordServiceStates(_ context.Context, deviceID string, states []servicestatus.ServiceState, reportedAt time.Time) error {
	// Defensive copy so later mutations of the input slice don't change
	// what the test sees.
	cp := make([]servicestatus.ServiceState, len(states))
	copy(cp, states)
	f.calls = append(f.calls, serviceStatusWriteCall{deviceID, cp, reportedAt})
	return f.err
}

// Tracer bullet for the cp-ingest service-status handler: a valid
// report with two services and a known device → writer called once
// with the right device_id, the right per-service rows, and the
// cp-side ingest timestamp (not the agent's ReportedAt, since agent
// clocks can drift).
func TestServiceStatusIngesterHappyPath(t *testing.T) {
	ingestAt := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	agentAt := ingestAt.Add(-3 * time.Second) // simulate small drift
	w := &fakeServiceStatusWriter{}
	ing := NewServiceStatusIngester(w, fixedClock(ingestAt))

	report := servicestatus.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		ReportedAt:    agentAt,
		Services: []servicestatus.ServiceState{
			{Name: "com.uknomi.edge-ui", State: service.StateRunning, StateSince: agentAt.Add(-2 * time.Hour)},
			{Name: "nginx", State: service.StateStopped, StateSince: agentAt.Add(-30 * time.Second)},
		},
	}

	if err := ing.Handle(context.Background(), report); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(w.calls) != 1 {
		t.Fatalf("RecordServiceStates calls: got %d want 1", len(w.calls))
	}
	got := w.calls[0]
	if got.deviceID != "dev-1" {
		t.Errorf("device_id: got %q want %q", got.deviceID, "dev-1")
	}
	if !got.reportedAt.Equal(ingestAt) {
		t.Errorf("reportedAt: got %v want %v (cp-side ingest time, not agent ReportedAt)", got.reportedAt, ingestAt)
	}
	if len(got.states) != 2 {
		t.Fatalf("states: got %d entries want 2", len(got.states))
	}
	// Per-service contents are pass-through; agent's StateSince must
	// arrive at storage intact so the dashboard's "running since N hours"
	// display works.
	if got.states[0].StateSince.IsZero() || got.states[1].StateSince.IsZero() {
		t.Errorf("StateSince zeroed in transit: got %+v", got.states)
	}
}

// Empty DeviceID is permanent — the IoT Rule should have stamped it,
// so absence means a malformed payload that no amount of redelivery
// will fix. Poison to DLQ.
func TestServiceStatusIngesterEmptyDeviceIDIsPoison(t *testing.T) {
	w := &fakeServiceStatusWriter{}
	ing := NewServiceStatusIngester(w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), servicestatus.Report{
		DeviceID:      "",
		CorrelationID: "corr-1",
		Services:      []servicestatus.ServiceState{{Name: "nginx", State: service.StateRunning}},
	})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("empty device_id: got %v want a poison error", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("writer was called for a poison message: %+v", w.calls)
	}
}

// A report naming a device that no longer exists in the registry must
// not redeliver forever — the device was likely decommissioned between
// the agent's last publish and our consume. Poison.
func TestServiceStatusIngesterUnknownDeviceIsPoison(t *testing.T) {
	w := &fakeServiceStatusWriter{err: registry.ErrDeviceNotFound}
	ing := NewServiceStatusIngester(w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), servicestatus.Report{
		DeviceID:      "ghost",
		CorrelationID: "corr-1",
		Services:      []servicestatus.ServiceState{{Name: "nginx", State: service.StateRunning}},
	})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("unknown device: got %v want a poison error", err)
	}
}

// Transient writer errors (DB unreachable, etc.) must propagate as
// plain errors so SQS redelivers. Distinct from the poison cases above.
func TestServiceStatusIngesterTransientErrorRedelivers(t *testing.T) {
	transient := errors.New("connection refused")
	w := &fakeServiceStatusWriter{err: transient}
	ing := NewServiceStatusIngester(w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), servicestatus.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		Services:      []servicestatus.ServiceState{{Name: "nginx", State: service.StateRunning}},
	})
	if err == nil {
		t.Fatal("expected the transient error to propagate; got nil")
	}
	if errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("transient error became poison; got %v", err)
	}
	if !errors.Is(err, transient) {
		t.Errorf("expected underlying error preserved; got %v", err)
	}
}
