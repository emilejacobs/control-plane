package audit_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

// TestWriteCapturesEntryAndCorrelationID is the tracer bullet for Issue 20:
// an in-memory Writer records the Entry's structured fields verbatim and
// stamps the correlation_id from the cplog request-scoped logger context.
// The PostgresWriter and the slog co-emission both build on this contract.
func TestWriteCapturesEntryAndCorrelationID(t *testing.T) {
	mem := &audit.MemoryWriter{}
	ctx := cplog.WithCorrelationID(context.Background(), "corr-123")

	err := mem.Write(ctx, audit.Entry{
		Action:    "audit.test",
		ActorID:   "op-1",
		ActorType: audit.ActorOperator,
		Outcome:   "success",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := mem.Entries()
	if len(got) != 1 {
		t.Fatalf("entries: got %d, want 1", len(got))
	}
	if got[0].Action != "audit.test" {
		t.Errorf("Action: got %q, want %q", got[0].Action, "audit.test")
	}
	if got[0].ActorType != audit.ActorOperator {
		t.Errorf("ActorType: got %q, want %q", got[0].ActorType, audit.ActorOperator)
	}
	if got[0].CorrelationID != "corr-123" {
		t.Errorf("CorrelationID: got %q, want %q", got[0].CorrelationID, "corr-123")
	}
}
