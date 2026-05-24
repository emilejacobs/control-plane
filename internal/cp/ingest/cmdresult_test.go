package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/envelope"
)

type recordingApplier struct {
	mu    sync.Mutex
	calls []applyArgs
	err   error
}
type applyArgs struct {
	deviceID, correlationID string
	at                      time.Time
}

func (r *recordingApplier) RecordServiceConfigApplied(_ context.Context, deviceID, correlationID string, at time.Time) error {
	r.mu.Lock()
	r.calls = append(r.calls, applyArgs{deviceID: deviceID, correlationID: correlationID, at: at})
	r.mu.Unlock()
	return r.err
}

// Happy path: a successful config.update ACK lands → ingester calls
// RecordServiceConfigApplied with the cp-side wall-clock ingest time,
// the device_id (injected by the IoT Rule's topic(2)), and the
// correlation_id from the envelope.
func TestCmdResultConfigUpdateSuccess(t *testing.T) {
	applier := &recordingApplier{}
	now := time.Date(2026, 5, 24, 19, 30, 0, 0, time.UTC)
	ing := ingest.NewCmdResultIngester(applier, func() time.Time { return now })

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-xyz",
			CommandID:     "cmd-1",
			Type:          "config.update",
			Success:       true,
		},
		DeviceID: "dev-abc",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.calls) != 1 {
		t.Fatalf("RecordServiceConfigApplied calls: got %d, want 1", len(applier.calls))
	}
	c := applier.calls[0]
	if c.deviceID != "dev-abc" || c.correlationID != "corr-xyz" || !c.at.Equal(now) {
		t.Errorf("RecordServiceConfigApplied args: got %+v, want dev-abc / corr-xyz / %v", c, now)
	}
}

// Failure ACK (Success=false) is logged + recorded as a no-op write;
// last_applied stays whatever it was. Captured via the absence of a
// RecordServiceConfigApplied call. The handler must NOT return an
// error — the message was successfully handled (it's just bad news).
func TestCmdResultConfigUpdateFailureIsLoggedNoWrite(t *testing.T) {
	applier := &recordingApplier{}
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ing := ingest.NewCmdResultIngester(applier, nil)
	ing.Logger = logger

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-bad",
			Type:          "config.update",
			Success:       false,
			Error:         &envelope.ResultError{Code: "config_update.bad_interval", Message: "interval 1s outside 30s..1h0m0s"},
		},
		DeviceID: "dev-abc",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.calls) != 0 {
		t.Errorf("RecordServiceConfigApplied should not be called on failure ACK; got %d", len(applier.calls))
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("config_update.bad_interval")) {
		t.Errorf("expected the error code in the log; got: %s", logBuf.String())
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("dev-abc")) {
		t.Errorf("expected device_id in the log; got: %s", logBuf.String())
	}
}

// ACKs for other command types (e.g. heartbeat, service.status) are
// out-of-scope for slice 2's handler — silently ignored (no log, no
// write). Future cycles will add type-specific routing for Phase 3
// command results.
func TestCmdResultUnknownTypeIgnored(t *testing.T) {
	applier := &recordingApplier{}
	ing := ingest.NewCmdResultIngester(applier, nil)
	msg := ingest.CmdResult{
		Result:   envelope.Result{CorrelationID: "c", Type: "service.restart", Success: true},
		DeviceID: "dev-abc",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.calls) != 0 {
		t.Errorf("RecordServiceConfigApplied called for non-config.update type; got %d", len(applier.calls))
	}
}

// An ACK from an unknown device (decommissioned, or a stale one re-
// delivered) returns Poison so the SQS consumer DLQs it instead of
// looping. Mirrors the heartbeat / service-status ingester pattern.
func TestCmdResultUnknownDeviceIsPoison(t *testing.T) {
	applier := &recordingApplier{err: registry.ErrDeviceNotFound}
	ing := ingest.NewCmdResultIngester(applier, nil)
	msg := ingest.CmdResult{
		Result:   envelope.Result{CorrelationID: "c", Type: "config.update", Success: true},
		DeviceID: "dev-gone",
	}
	err := ing.Handle(context.Background(), msg)
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("expected poison error, got %v", err)
	}
}

// Empty device_id (IoT Rule failed to inject topic(2), or someone
// published manually) is also poison — there's nothing to apply to.
func TestCmdResultEmptyDeviceIsPoison(t *testing.T) {
	applier := &recordingApplier{}
	ing := ingest.NewCmdResultIngester(applier, nil)
	msg := ingest.CmdResult{
		Result:   envelope.Result{CorrelationID: "c", Type: "config.update", Success: true},
		DeviceID: "",
	}
	err := ing.Handle(context.Background(), msg)
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("expected poison error, got %v", err)
	}
}

// CmdResult must implement sqsconsumer.Correlated so the generic
// consumer can route it. The compile-time check below is the contract.
func TestCmdResultIsCorrelated(t *testing.T) {
	var _ sqsconsumer.Correlated = ingest.CmdResult{}
	c := ingest.CmdResult{Result: envelope.Result{CorrelationID: "abc"}}
	if c.Correlation() != "abc" {
		t.Errorf("Correlation: got %q want abc", c.Correlation())
	}
}

// Wire-shape sanity: a JSON envelope with topic-derived device_id
// round-trips through the CmdResult unmarshaller. This is exactly the
// shape the IoT Rule's `SELECT *, topic(2) as device_id` produces.
func TestCmdResultJSONUnmarshal(t *testing.T) {
	raw := []byte(`{
		"device_id": "dev-from-topic",
		"correlation_id": "corr-1",
		"command_id": "cmd-1",
		"type": "config.update",
		"success": true,
		"result": {"applied_at": "2026-05-24T19:35:00Z"}
	}`)
	var cr ingest.CmdResult
	if err := json.Unmarshal(raw, &cr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cr.DeviceID != "dev-from-topic" {
		t.Errorf("DeviceID: got %q", cr.DeviceID)
	}
	if cr.CorrelationID != "corr-1" || cr.Type != "config.update" || !cr.Success {
		t.Errorf("envelope fields wrong: %+v", cr)
	}
}
