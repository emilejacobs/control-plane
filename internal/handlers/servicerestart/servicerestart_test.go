package servicerestart_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/dispatcher"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/handlers/servicerestart"
	"github.com/emilejacobs/control-plane/internal/service"
)

func TestRestartReturnsTimestampsOnSuccess(t *testing.T) {
	backend := &service.Fake{} // empty RestartErrors → all restarts succeed
	h := servicerestart.New(backend)

	before := time.Now()
	result, err := h.Handle(context.Background(), json.RawMessage(`{"name": "nginx"}`))
	after := time.Now()
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	resp, ok := result.(servicerestart.Response)
	if !ok {
		t.Fatalf("expected servicerestart.Response, got %T", result)
	}
	if resp.Name != "nginx" {
		t.Errorf("Name: got %q, want nginx", resp.Name)
	}
	if resp.StartedAt.Before(before) || resp.StartedAt.After(after) {
		t.Errorf("StartedAt %v outside [%v, %v]", resp.StartedAt, before, after)
	}
	if resp.FinishedAt.Before(resp.StartedAt) {
		t.Errorf("FinishedAt %v before StartedAt %v", resp.FinishedAt, resp.StartedAt)
	}
	if resp.FinishedAt.After(after) {
		t.Errorf("FinishedAt %v after test boundary %v", resp.FinishedAt, after)
	}
}

func TestRestartFailureSurfacesAsServiceRestartFailedCode(t *testing.T) {
	backend := &service.Fake{
		RestartErrors: map[string]error{
			"flaky": &service.ExecError{
				ExitCode: 5,
				Stderr:   "Unit flaky.service not loaded.",
			},
		},
	}
	d := dispatcher.New()
	d.Register("service.restart", servicerestart.New(backend))

	cmd := envelope.Command{
		CorrelationID: "corr",
		CommandID:     "cmdid",
		Type:          "service.restart",
		Args:          json.RawMessage(`{"name": "flaky"}`),
		IssuedAt:      time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
	}
	cmdBytes, _ := json.Marshal(cmd)

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
	if result.Error.Code != "service.restart_failed" {
		t.Errorf("Error.Code: got %q, want service.restart_failed", result.Error.Code)
	}
	if !strings.Contains(result.Error.Message, "Unit flaky.service not loaded.") {
		t.Errorf("Error.Message should contain stderr; got %q", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "exit 5") {
		t.Errorf("Error.Message should mention exit code 5; got %q", result.Error.Message)
	}
}
