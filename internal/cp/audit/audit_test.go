package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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

// TestWriteCoEmitsSlogLineInLegacyShape locks the slog co-emission contract:
// audit.Write produces a JSON log line whose msg is the action, with the
// well-known fields (outcome, source_ip, user_agent) as top-level attrs and
// every Payload key flattened. The existing 34 audit.* tests grep these
// top-level attrs out of slog JSON — they must keep passing when call sites
// migrate from raw log.Info to audit.Write.
func TestWriteCoEmitsSlogLineInLegacyShape(t *testing.T) {
	var buf bytes.Buffer
	logger := cplog.New(&buf, "audit-test")
	ctx := cplog.WithLogger(context.Background(), logger)

	if err := (&audit.MemoryWriter{}).Write(ctx, audit.Entry{
		Action:    "audit.enrollment",
		Outcome:   "success",
		SourceIP:  "192.0.2.1",
		UserAgent: "agent/1.0",
		Payload: map[string]any{
			"hostname":      "mac-mini-acme-01",
			"hardware_uuid": "11111111-2222-3333-4444-555555555555",
		},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	line := firstJSONLine(t, buf.String(), "audit.enrollment")
	mustEqual(t, line, "outcome", "success")
	mustEqual(t, line, "source_ip", "192.0.2.1")
	mustEqual(t, line, "user_agent", "agent/1.0")
	mustEqual(t, line, "hostname", "mac-mini-acme-01")
	mustEqual(t, line, "hardware_uuid", "11111111-2222-3333-4444-555555555555")
}

func firstJSONLine(t *testing.T, buf, wantMsg string) map[string]any {
	t.Helper()
	for _, raw := range strings.Split(buf, "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			continue
		}
		if line["msg"] == wantMsg {
			return line
		}
	}
	t.Fatalf("no slog line with msg=%q in:\n%s", wantMsg, buf)
	return nil
}

func mustEqual(t *testing.T, line map[string]any, key string, want any) {
	t.Helper()
	if got := line[key]; got != want {
		t.Errorf("attr %q: got %v, want %v", key, got, want)
	}
}
