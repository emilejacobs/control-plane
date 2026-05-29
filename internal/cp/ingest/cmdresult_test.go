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
	mu                     sync.Mutex
	calls                  []applyArgs
	logTailCompletes       []registry.LogTailCompletion
	logTailFailures        []registry.LogTailFailure
	camerasCalls           []applyArgs
	networkScanCompletes   []registry.NetworkScanCompletion
	networkScanFailures    []registry.NetworkScanFailure
	err                    error
	logTailCompleteErr     error
	logTailFailErr         error
	camerasErr             error
	networkScanCompleteErr error
	networkScanFailErr     error
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

func (r *recordingApplier) CompleteLogTail(_ context.Context, c registry.LogTailCompletion) error {
	r.mu.Lock()
	r.logTailCompletes = append(r.logTailCompletes, c)
	r.mu.Unlock()
	return r.logTailCompleteErr
}

func (r *recordingApplier) FailLogTail(_ context.Context, f registry.LogTailFailure) error {
	r.mu.Lock()
	r.logTailFailures = append(r.logTailFailures, f)
	r.mu.Unlock()
	return r.logTailFailErr
}

func (r *recordingApplier) RecordCamerasApplied(_ context.Context, deviceID, correlationID string, at time.Time) error {
	r.mu.Lock()
	r.camerasCalls = append(r.camerasCalls, applyArgs{deviceID: deviceID, correlationID: correlationID, at: at})
	r.mu.Unlock()
	return r.camerasErr
}

func (r *recordingApplier) CompleteNetworkScan(_ context.Context, c registry.NetworkScanCompletion) error {
	r.mu.Lock()
	r.networkScanCompletes = append(r.networkScanCompletes, c)
	r.mu.Unlock()
	return r.networkScanCompleteErr
}

func (r *recordingApplier) FailNetworkScan(_ context.Context, f registry.NetworkScanFailure) error {
	r.mu.Lock()
	r.networkScanFailures = append(r.networkScanFailures, f)
	r.mu.Unlock()
	return r.networkScanFailErr
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

// Phase 2 slice 3: log.tail success ACK → CompleteLogTail called with
// content + truncation parsed from envelope.Result.Result.
func TestCmdResultLogTailSuccess(t *testing.T) {
	applier := &recordingApplier{}
	now := time.Date(2026, 5, 24, 22, 0, 0, 0, time.UTC)
	ing := ingest.NewCmdResultIngester(applier, func() time.Time { return now })

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-lt-1",
			Type:          "log.tail",
			Success:       true,
			Result:        []byte(`{"content":"line1\nline2\n","truncated":true,"truncated_from":500}`),
		},
		DeviceID: "dev-abc",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.logTailCompletes) != 1 {
		t.Fatalf("CompleteLogTail calls: got %d, want 1", len(applier.logTailCompletes))
	}
	c := applier.logTailCompletes[0]
	if c.CorrelationID != "corr-lt-1" || c.Content != "line1\nline2\n" {
		t.Errorf("CompleteLogTail args: got %+v", c)
	}
	if !c.Truncated || c.TruncatedFrom != 500 {
		t.Errorf("truncation: got truncated=%v from=%d", c.Truncated, c.TruncatedFrom)
	}
	if !c.ReturnedAt.Equal(now) {
		t.Errorf("ReturnedAt: got %v, want %v", c.ReturnedAt, now)
	}
}

// Failure ACK → FailLogTail called with error code/message from
// envelope.Result.Error. CompleteLogTail must NOT be called.
func TestCmdResultLogTailFailure(t *testing.T) {
	applier := &recordingApplier{}
	now := time.Date(2026, 5, 24, 22, 0, 0, 0, time.UTC)
	ing := ingest.NewCmdResultIngester(applier, func() time.Time { return now })

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-lt-bad",
			Type:          "log.tail",
			Success:       false,
			Error:         &envelope.ResultError{Code: "log_tail.unknown_log", Message: "not in allow-list"},
		},
		DeviceID: "dev-abc",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.logTailCompletes) != 0 {
		t.Error("CompleteLogTail should not fire on failure ACK")
	}
	if len(applier.logTailFailures) != 1 {
		t.Fatalf("FailLogTail calls: got %d, want 1", len(applier.logTailFailures))
	}
	f := applier.logTailFailures[0]
	if f.ErrorCode != "log_tail.unknown_log" || f.ErrorMessage != "not in allow-list" {
		t.Errorf("FailLogTail args: got %+v", f)
	}
}

// Unknown log_tail row (ErrLogTailNotFound) → Poison. The request
// row went away (sweeper ran early); no point retrying.
func TestCmdResultLogTailUnknownRowIsPoison(t *testing.T) {
	applier := &recordingApplier{logTailCompleteErr: registry.ErrLogTailNotFound}
	ing := ingest.NewCmdResultIngester(applier, nil)
	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-gone",
			Type:          "log.tail",
			Success:       true,
			Result:        []byte(`{"content":"x"}`),
		},
		DeviceID: "dev-abc",
	}
	err := ing.Handle(context.Background(), msg)
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("expected poison error, got %v", err)
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

// Happy path: a successful cameras.update ACK lands → ingester
// calls RecordCamerasApplied with the cp-side wall-clock ingest
// time + device_id + correlation_id. Mirrors the config.update
// pattern from slice 2 (Edge UI rework, issue #2).
func TestCmdResultCamerasUpdateSuccess(t *testing.T) {
	applier := &recordingApplier{}
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	ing := ingest.NewCmdResultIngester(applier, func() time.Time { return now })

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-cam-1",
			CommandID:     "cmd-cam-1",
			Type:          "cameras.update",
			Success:       true,
		},
		DeviceID: "dev-cam",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.camerasCalls) != 1 {
		t.Fatalf("RecordCamerasApplied calls: got %d want 1", len(applier.camerasCalls))
	}
	c := applier.camerasCalls[0]
	if c.deviceID != "dev-cam" || c.correlationID != "corr-cam-1" || !c.at.Equal(now) {
		t.Errorf("RecordCamerasApplied args: got %+v", c)
	}
}

// Happy path: a successful network.scan ACK lands → ingester calls
// CompleteNetworkScan with the hosts list parsed out of the
// envelope's Result payload and the cp-side wall-clock ingest time.
func TestCmdResultNetworkScanSuccess(t *testing.T) {
	applier := &recordingApplier{}
	now := time.Date(2026, 5, 26, 15, 0, 0, 0, time.UTC)
	ing := ingest.NewCmdResultIngester(applier, func() time.Time { return now })

	resultPayload := json.RawMessage(`{
		"hosts": [
			{"ip":"192.168.1.10","mac":"44:19:b6:aa:bb:cc","vendor":"Hikvision","open_ports":[80,554]},
			{"ip":"192.168.1.42","mac":"3c:ef:8c:11:22:33","vendor":"Dahua","open_ports":[443]}
		]
	}`)
	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-ns-1",
			CommandID:     "cmd-ns-1",
			Type:          "network.scan",
			Success:       true,
			Result:        resultPayload,
		},
		DeviceID: "dev-ns",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.networkScanCompletes) != 1 {
		t.Fatalf("CompleteNetworkScan calls: got %d, want 1", len(applier.networkScanCompletes))
	}
	c := applier.networkScanCompletes[0]
	if c.CorrelationID != "corr-ns-1" {
		t.Errorf("CorrelationID: got %q", c.CorrelationID)
	}
	if !c.ReturnedAt.Equal(now) {
		t.Errorf("ReturnedAt: got %v, want %v", c.ReturnedAt, now)
	}
	if len(c.Hosts) != 2 || c.Hosts[0].Vendor != "Hikvision" || c.Hosts[1].Vendor != "Dahua" {
		t.Errorf("hosts: got %+v", c.Hosts)
	}
}

// Failure ACK: ingester routes to FailNetworkScan with the agent's
// error code/message. The pending row transitions to status=error in
// the registry; no completion write happens.
func TestCmdResultNetworkScanFailure(t *testing.T) {
	applier := &recordingApplier{}
	now := time.Date(2026, 5, 26, 15, 5, 0, 0, time.UTC)
	ing := ingest.NewCmdResultIngester(applier, func() time.Time { return now })

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-ns-fail",
			CommandID:     "cmd-ns-fail",
			Type:          "network.scan",
			Success:       false,
			Error: &envelope.ResultError{
				Code:    "network_scan.scan_failed",
				Message: "arp-scan: command not found",
			},
		},
		DeviceID: "dev-ns",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle on failure ACK: got %v, want nil (handled)", err)
	}
	if len(applier.networkScanFailures) != 1 {
		t.Fatalf("FailNetworkScan calls: got %d, want 1", len(applier.networkScanFailures))
	}
	f := applier.networkScanFailures[0]
	if f.CorrelationID != "corr-ns-fail" {
		t.Errorf("CorrelationID: got %q", f.CorrelationID)
	}
	if f.ErrorCode != "network_scan.scan_failed" {
		t.Errorf("ErrorCode: got %q", f.ErrorCode)
	}
	if !f.ReturnedAt.Equal(now) {
		t.Errorf("ReturnedAt: got %v, want %v", f.ReturnedAt, now)
	}
	if len(applier.networkScanCompletes) != 0 {
		t.Error("CompleteNetworkScan should not be called on failure ACK")
	}
}

// ErrNetworkScanNotFound returned by CompleteNetworkScan / FailNetworkScan
// is poison — the request row went away (sweeper ran early). No point
// retrying.
func TestCmdResultNetworkScanCompleteRowGone(t *testing.T) {
	applier := &recordingApplier{networkScanCompleteErr: registry.ErrNetworkScanNotFound}
	ing := ingest.NewCmdResultIngester(applier, time.Now)

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-ns-gone",
			Type:          "network.scan",
			Success:       true,
			Result:        json.RawMessage(`{"hosts":[]}`),
		},
		DeviceID: "dev-ns",
	}
	err := ing.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected poison error")
	}
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("expected wrapped ErrPoison, got %v", err)
	}
}

// Failure ACK: ingester logs but does NOT write — the override on
// CP stays as-is; the dashboard surfaces missing applied_at as
// "pending". Same posture as config.update.
func TestCmdResultCamerasUpdateFailure(t *testing.T) {
	applier := &recordingApplier{}
	ing := ingest.NewCmdResultIngester(applier, time.Now)

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-cam-fail",
			CommandID:     "cmd-cam-fail",
			Type:          "cameras.update",
			Success:       false,
			Error: &envelope.ResultError{
				Code:    "cameras.apply_failed",
				Message: "disk full",
			},
		},
		DeviceID: "dev-cam",
	}
	if err := ing.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle on failure ACK: got error %v, want nil (handled)", err)
	}
	if len(applier.camerasCalls) != 0 {
		t.Errorf("RecordCamerasApplied should not be called on failure ACK; got %d", len(applier.camerasCalls))
	}
}
