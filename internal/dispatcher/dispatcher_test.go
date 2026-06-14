package dispatcher_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/dispatcher"
	"github.com/emilejacobs/control-plane/internal/envelope"
)

func TestDispatcherRoutesToRegisteredHandler(t *testing.T) {
	d := dispatcher.New()
	d.Register("ping", dispatcher.HandlerFunc(func(ctx context.Context, args json.RawMessage) (any, error) {
		return map[string]string{"pong": "ok"}, nil
	}))

	cmd := envelope.Command{
		Type:     "ping",
		IssuedAt: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal cmd: %v", err)
	}

	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var result envelope.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !result.Success {
		t.Fatalf("expected success=true, got: %+v", result)
	}

	var payload map[string]string
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["pong"] != "ok" {
		t.Fatalf("expected payload.pong=ok, got: %+v", payload)
	}
}

func TestDispatcherEchoesCorrelationAndCommandID(t *testing.T) {
	d := dispatcher.New()
	d.Register("ping", dispatcher.HandlerFunc(func(ctx context.Context, args json.RawMessage) (any, error) {
		return nil, nil
	}))

	cmd := envelope.Command{
		CorrelationID: "corr-123",
		CommandID:     "cmd-456",
		Type:          "ping",
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var result envelope.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.CorrelationID != cmd.CorrelationID {
		t.Errorf("CorrelationID: got %q, want %q", result.CorrelationID, cmd.CorrelationID)
	}
	if result.CommandID != cmd.CommandID {
		t.Errorf("CommandID: got %q, want %q", result.CommandID, cmd.CommandID)
	}
	if result.Type != cmd.Type {
		t.Errorf("Type: got %q, want %q (Phase 2 slice 2: cp-ingest cmd-result handler routes by Type)", result.Type, cmd.Type)
	}
}

// Failure paths also echo Type so cp-side can route a failure ACK to
// the right per-command-type handler (and surface the error code).
func TestDispatcherFailureResultEchoesType(t *testing.T) {
	d := dispatcher.New()
	d.Register("explode", dispatcher.HandlerFunc(func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, envelope.NewCodedError("explode.boom", "kaboom")
	}))
	cmd := envelope.Command{Type: "explode", CorrelationID: "c", CommandID: "x"}
	cmdBytes, _ := json.Marshal(cmd)
	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var result envelope.Result
	_ = json.Unmarshal(resultBytes, &result)
	if result.Success {
		t.Fatal("expected failure")
	}
	if result.Type != "explode" {
		t.Errorf("Type on failure: got %q, want explode", result.Type)
	}
}

func TestDispatcherLogsCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	d := dispatcher.New(dispatcher.WithLogger(logger))
	d.Register("ping", dispatcher.HandlerFunc(func(ctx context.Context, args json.RawMessage) (any, error) {
		return nil, nil
	}))

	cmd := envelope.Command{
		CorrelationID: "log-correlation-test",
		CommandID:     "cmd-789",
		Type:          "ping",
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := d.Dispatch(context.Background(), cmdBytes); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	scanner := bufio.NewScanner(&buf)
	var matched int
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("invalid JSON log line %q: %v", scanner.Text(), err)
		}
		if entry["correlation_id"] == "log-correlation-test" {
			matched++
		}
	}
	if matched == 0 {
		t.Fatalf("no log line with correlation_id=log-correlation-test; output:\n%s", buf.String())
	}
}

func TestDispatcherRejectsUnknownCommandType(t *testing.T) {
	d := dispatcher.New()

	cmd := envelope.Command{
		CorrelationID: "corr",
		CommandID:     "cmdid",
		Type:          "no-such-command",
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var result envelope.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if result.Error == nil {
		t.Fatalf("expected error to be set, got nil")
	}
	if result.Error.Code != "command.unknown_type" {
		t.Errorf("Error.Code: got %q, want %q", result.Error.Code, "command.unknown_type")
	}
	if result.CorrelationID != cmd.CorrelationID {
		t.Errorf("CorrelationID should propagate on failure too: got %q", result.CorrelationID)
	}
}

func TestDispatcherCatchesHandlerPanic(t *testing.T) {
	d := dispatcher.New()
	d.Register("explode", dispatcher.HandlerFunc(func(ctx context.Context, args json.RawMessage) (any, error) {
		panic("kaboom")
	}))

	cmd := envelope.Command{
		CorrelationID: "corr",
		CommandID:     "cmdid",
		Type:          "explode",
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch should not propagate panic as error: %v", err)
	}

	var result envelope.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Success {
		t.Errorf("expected success=false on panic, got true")
	}
	if result.Error == nil || result.Error.Code != "handler.panic" {
		t.Errorf("expected Error.Code=handler.panic, got %+v", result.Error)
	}
	if result.CorrelationID != cmd.CorrelationID {
		t.Errorf("CorrelationID should propagate on panic: got %q", result.CorrelationID)
	}
}

func TestDispatcherUsesCodedErrorCode(t *testing.T) {
	d := dispatcher.New()
	d.Register("coded", dispatcher.HandlerFunc(func(ctx context.Context, args json.RawMessage) (any, error) {
		return nil, envelope.NewCodedError("widget.frobnicated", "widget was frobnicated")
	}))

	cmd := envelope.Command{
		CorrelationID: "corr",
		CommandID:     "cmdid",
		Type:          "coded",
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var result envelope.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if result.Error == nil {
		t.Fatalf("expected error to be set")
	}
	if result.Error.Code != "widget.frobnicated" {
		t.Errorf("Error.Code: got %q, want widget.frobnicated", result.Error.Code)
	}
	if result.Error.Message != "widget was frobnicated" {
		t.Errorf("Error.Message: got %q, want widget was frobnicated", result.Error.Message)
	}
}

func TestDispatcherWrapsPlainErrorAsHandlerError(t *testing.T) {
	d := dispatcher.New()
	d.Register("plain", dispatcher.HandlerFunc(func(ctx context.Context, args json.RawMessage) (any, error) {
		return nil, errors.New("something broke")
	}))

	cmd := envelope.Command{Type: "plain", IssuedAt: time.Now()}
	cmdBytes, _ := json.Marshal(cmd)
	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var result envelope.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Error == nil || result.Error.Code != "handler.error" {
		t.Errorf("expected Error.Code=handler.error, got %+v", result.Error)
	}
}

func TestDispatcherHandlerReturnedError(t *testing.T) {
	d := dispatcher.New()
	d.Register("fail", dispatcher.HandlerFunc(func(ctx context.Context, args json.RawMessage) (any, error) {
		return nil, errors.New("something broke")
	}))

	cmd := envelope.Command{
		CorrelationID: "corr",
		CommandID:     "cmdid",
		Type:          "fail",
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resultBytes, err := d.Dispatch(context.Background(), cmdBytes)
	if err != nil {
		t.Fatalf("dispatch should not propagate handler errors as Go errors: %v", err)
	}

	var result envelope.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Success {
		t.Errorf("expected success=false, got true")
	}
	if result.Error == nil || result.Error.Code != "handler.error" {
		t.Errorf("expected Error.Code=handler.error, got %+v", result.Error)
	}
	if !strings.Contains(result.Error.Message, "something broke") {
		t.Errorf("expected error message to contain 'something broke', got %q", result.Error.Message)
	}
}

// --- issue #41: signature-verification gate ---

// A required-signature command type runs its handler only when the injected
// verifier accepts the command; a missing or invalid signature is rejected
// with command.bad_signature and the handler never runs.
func TestDispatcherGatesRequiredSignature(t *testing.T) {
	verify := func(cmd envelope.Command) error {
		if cmd.Signature != nil && *cmd.Signature == "good" {
			return nil
		}
		return errors.New("bad signature")
	}

	run := func(t *testing.T, sig *string) (envelope.Result, bool) {
		t.Helper()
		var handlerRan bool
		d := dispatcher.New(dispatcher.WithSignatureVerification(verify, "agent.update"))
		d.Register("agent.update", dispatcher.HandlerFunc(func(context.Context, json.RawMessage) (any, error) {
			handlerRan = true
			return map[string]string{"ok": "1"}, nil
		}))
		cmd := envelope.Command{Type: "agent.update", CommandID: "c1", Signature: sig}
		raw, _ := json.Marshal(cmd)
		out, err := d.Dispatch(context.Background(), raw)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		var res envelope.Result
		_ = json.Unmarshal(out, &res)
		return res, handlerRan
	}

	good := "good"
	if res, ran := run(t, &good); !res.Success || !ran {
		t.Errorf("valid signature: success=%v ran=%v, want true/true (%+v)", res.Success, ran, res.Error)
	}

	bad := "bad"
	if res, ran := run(t, &bad); res.Success || ran {
		t.Errorf("invalid signature: success=%v ran=%v, want false/false", res.Success, ran)
	} else if res.Error == nil || res.Error.Code != "command.bad_signature" {
		t.Errorf("invalid signature: error = %+v, want command.bad_signature", res.Error)
	}

	if res, ran := run(t, nil); res.Success || ran {
		t.Errorf("missing signature: success=%v ran=%v, want false/false", res.Success, ran)
	} else if res.Error == nil || res.Error.Code != "command.bad_signature" {
		t.Errorf("missing signature: error = %+v, want command.bad_signature", res.Error)
	}
}

// A command type that is NOT in the required-signature set runs unsigned —
// forward-compat with the Phase 0/2 unsigned handlers (ADR-028). The verifier
// is never consulted for it.
func TestDispatcherForwardCompatUnsignedNonRequired(t *testing.T) {
	verifyCalled := false
	verify := func(envelope.Command) error {
		verifyCalled = true
		return errors.New("would reject")
	}
	var handlerRan bool
	d := dispatcher.New(dispatcher.WithSignatureVerification(verify, "agent.update"))
	d.Register("config.update", dispatcher.HandlerFunc(func(context.Context, json.RawMessage) (any, error) {
		handlerRan = true
		return nil, nil
	}))

	cmd := envelope.Command{Type: "config.update", CommandID: "c2"} // no signature
	raw, _ := json.Marshal(cmd)
	out, _ := d.Dispatch(context.Background(), raw)
	var res envelope.Result
	_ = json.Unmarshal(out, &res)

	if !res.Success || !handlerRan {
		t.Errorf("unsigned non-required cmd: success=%v ran=%v, want true/true", res.Success, handlerRan)
	}
	if verifyCalled {
		t.Error("verifier was consulted for a non-required command type")
	}
}

// A dispatcher built without the verification option leaves every command
// ungated (the default before #41 wires it in).
func TestDispatcherNoVerifierGatesNothing(t *testing.T) {
	var handlerRan bool
	d := dispatcher.New()
	d.Register("agent.update", dispatcher.HandlerFunc(func(context.Context, json.RawMessage) (any, error) {
		handlerRan = true
		return nil, nil
	}))
	cmd := envelope.Command{Type: "agent.update", CommandID: "c3"} // unsigned
	raw, _ := json.Marshal(cmd)
	out, _ := d.Dispatch(context.Background(), raw)
	var res envelope.Result
	_ = json.Unmarshal(out, &res)
	if !res.Success || !handlerRan {
		t.Errorf("no-verifier dispatcher: success=%v ran=%v, want true/true", res.Success, handlerRan)
	}
}
