package ingest

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

type healthProbeWriteCall struct {
	deviceID   string
	results    []healthprobes.Result
	observedAt time.Time
}

type fakeHealthProbeWriter struct {
	err   error
	calls []healthProbeWriteCall
}

func (f *fakeHealthProbeWriter) RecordHealthProbes(_ context.Context, deviceID string, results []healthprobes.Result, observedAt time.Time) error {
	cp := make([]healthprobes.Result, len(results))
	copy(cp, results)
	f.calls = append(f.calls, healthProbeWriteCall{deviceID, cp, observedAt})
	return f.err
}

func TestHealthProbeIngesterHappyPath(t *testing.T) {
	ingestAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	agentAt := ingestAt.Add(-3 * time.Second)
	w := &fakeHealthProbeWriter{}
	ing := NewHealthProbeIngester(w, fixedClock(ingestAt))

	report := healthprobes.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		ReportedAt:    agentAt,
		Probes: []healthprobes.Result{
			{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
			{Name: healthprobes.ProbeGUISession, Status: healthprobes.StatusRed, State: "login_window"},
		},
	}
	if err := ing.Handle(context.Background(), report); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.calls) != 1 {
		t.Fatalf("RecordHealthProbes calls: got %d want 1", len(w.calls))
	}
	got := w.calls[0]
	if got.deviceID != "dev-1" {
		t.Errorf("device_id: got %q want dev-1", got.deviceID)
	}
	// cp-side wall clock is authoritative, not the agent's ReportedAt.
	if !got.observedAt.Equal(ingestAt) {
		t.Errorf("observedAt: got %v want %v", got.observedAt, ingestAt)
	}
	if len(got.results) != 2 {
		t.Errorf("results: got %d want 2", len(got.results))
	}
}

func TestHealthProbeIngesterEmptyDeviceIDIsPoison(t *testing.T) {
	w := &fakeHealthProbeWriter{}
	ing := NewHealthProbeIngester(w, fixedClock(time.Now()))
	err := ing.Handle(context.Background(), healthprobes.Report{CorrelationID: "c"})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Fatalf("err = %v, want poison", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("writer should not be called for empty device_id")
	}
}

func TestHealthProbeIngesterUnknownDeviceIsPoison(t *testing.T) {
	w := &fakeHealthProbeWriter{err: registry.ErrDeviceNotFound}
	ing := NewHealthProbeIngester(w, fixedClock(time.Now()))
	err := ing.Handle(context.Background(), healthprobes.Report{
		DeviceID: "dev-gone",
		Probes:   []healthprobes.Result{{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusRed, State: "missing"}},
	})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Fatalf("err = %v, want poison (DLQ a late report from a decommissioned device)", err)
	}
}

func TestHealthProbeIngesterTransientErrorRetries(t *testing.T) {
	w := &fakeHealthProbeWriter{err: errors.New("db down")}
	ing := NewHealthProbeIngester(w, fixedClock(time.Now()))
	err := ing.Handle(context.Background(), healthprobes.Report{
		DeviceID: "dev-1",
		Probes:   []healthprobes.Result{{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusRed, State: "missing"}},
	})
	if err == nil || errors.Is(err, sqsconsumer.ErrPoison) {
		t.Fatalf("err = %v, want a non-poison error so SQS redelivers", err)
	}
}

func TestHealthProbeIngesterLogsRedProbes(t *testing.T) {
	var buf bytes.Buffer
	ing := NewHealthProbeIngester(&fakeHealthProbeWriter{}, fixedClock(time.Now()))
	ing.Logger = slog.New(slog.NewJSONHandler(&buf, nil))

	report := healthprobes.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		Probes: []healthprobes.Result{
			{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
			{Name: healthprobes.ProbeUSBAudio, Status: healthprobes.StatusRed, State: "missing"},
		},
	}
	if err := ing.Handle(context.Background(), report); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "health-probe.red") || !strings.Contains(out, healthprobes.ProbeUSBAudio) {
		t.Errorf("expected a health-probe.red log line for usb_audio; got: %s", out)
	}
	if strings.Contains(out, healthprobes.ProbeAutoLogin) {
		t.Errorf("green probe should not be logged as red; got: %s", out)
	}
}
