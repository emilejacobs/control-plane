package servicestatus_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/dispatcher"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/handlers/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

func TestServiceStatusReturnsRunningForKnownService(t *testing.T) {
	backend := &service.Fake{
		States: map[string]service.State{
			"nginx": service.StateRunning,
		},
	}
	h := servicestatus.New(backend)

	args := json.RawMessage(`{"name": "nginx"}`)
	result, err := h.Handle(context.Background(), args)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	resp, ok := result.(servicestatus.Response)
	if !ok {
		t.Fatalf("expected servicestatus.Response, got %T", result)
	}
	if resp.Name != "nginx" {
		t.Errorf("Name: got %q, want nginx", resp.Name)
	}
	if resp.State != service.StateRunning {
		t.Errorf("State: got %q, want %q", resp.State, service.StateRunning)
	}
}

func TestServiceStatusReturnsStoppedForStoppedService(t *testing.T) {
	backend := &service.Fake{
		States: map[string]service.State{
			"nginx": service.StateStopped,
		},
	}
	h := servicestatus.New(backend)

	result, err := h.Handle(context.Background(), json.RawMessage(`{"name": "nginx"}`))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	resp := result.(servicestatus.Response)
	if resp.State != service.StateStopped {
		t.Errorf("State: got %q, want %q", resp.State, service.StateStopped)
	}
}

func TestServiceStatusReturnsNotFoundCodeForUnknownService(t *testing.T) {
	d := dispatcher.New()
	d.Register("service.status", servicestatus.New(&service.Fake{
		States: map[string]service.State{"nginx": service.StateRunning},
	}))

	cmd := envelope.Command{
		CorrelationID: "corr",
		CommandID:     "cmdid",
		Type:          "service.status",
		Args:          json.RawMessage(`{"name": "no-such-service"}`),
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
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
		t.Errorf("expected success=false for unknown service, got true")
	}
	if result.Error == nil || result.Error.Code != "service.not_found" {
		t.Errorf("expected Error.Code=service.not_found, got %+v", result.Error)
	}
	if result.CorrelationID != "corr" {
		t.Errorf("CorrelationID should propagate: got %q", result.CorrelationID)
	}
}
