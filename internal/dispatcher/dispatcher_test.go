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
