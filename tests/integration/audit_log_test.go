package integration_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

// TestPostgresWriterInsertsRow is the Issue 20 cycle 6 tracer: a write
// through audit.PostgresWriter lands as a queryable audit_log row with
// every Entry field intact, plus the correlation_id picked up from the
// cplog context. The migration must have applied; the row must persist.
func TestPostgresWriterInsertsRow(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	w := audit.NewPostgresWriter(pool)
	wctx := cplog.WithCorrelationID(ctx, "corr-issue-20-cycle-6")

	err := w.Write(wctx, audit.Entry{
		Action:       "audit.test",
		ActorID:      "operator-1",
		ActorType:    audit.ActorOperator,
		ResourceKind: "device",
		ResourceID:   "dev-1",
		SourceIP:     "10.0.0.1",
		UserAgent:    "test-suite/1.0",
		Outcome:      "success",
		Payload:      map[string]any{"email": "op@example.com", "reason": "tracer"},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	var (
		action, actorID, actorType, resourceKind, resourceID string
		correlationID, sourceIP, userAgent, outcome          string
		payload                                              map[string]any
	)
	err = pool.QueryRow(ctx, `
		SELECT action, actor_id, actor_type, resource_kind, resource_id,
		       correlation_id, source_ip, user_agent, outcome, payload
		FROM audit_log
		WHERE correlation_id = $1
	`, "corr-issue-20-cycle-6").Scan(
		&action, &actorID, &actorType, &resourceKind, &resourceID,
		&correlationID, &sourceIP, &userAgent, &outcome, &payload,
	)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}

	checks := []struct{ name, got, want string }{
		{"action", action, "audit.test"},
		{"actor_id", actorID, "operator-1"},
		{"actor_type", actorType, "operator"},
		{"resource_kind", resourceKind, "device"},
		{"resource_id", resourceID, "dev-1"},
		{"correlation_id", correlationID, "corr-issue-20-cycle-6"},
		{"source_ip", sourceIP, "10.0.0.1"},
		{"user_agent", userAgent, "test-suite/1.0"},
		{"outcome", outcome, "success"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if payload["email"] != "op@example.com" {
		t.Errorf("payload[email]: got %v, want op@example.com", payload["email"])
	}
	if payload["reason"] != "tracer" {
		t.Errorf("payload[reason]: got %v, want tracer", payload["reason"])
	}
}

// TestPostgresWriterDefaultsEmptyPayloadToJSONObject locks the
// nil-Payload edge case: the table column is NOT NULL with a '{}'
// default, but the INSERT explicitly passes the marshaled value. A nil
// Payload would marshal to "null" and break the jsonb column. Verify it
// becomes an empty object instead.
func TestPostgresWriterDefaultsEmptyPayloadToJSONObject(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	w := audit.NewPostgresWriter(pool)
	err := w.Write(ctx, audit.Entry{
		Action:    "audit.test.nil-payload",
		ActorType: audit.ActorSystem,
		Outcome:   "success",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	var raw string
	if err := pool.QueryRow(ctx, `
		SELECT payload::text FROM audit_log WHERE action = $1
	`, "audit.test.nil-payload").Scan(&raw); err != nil {
		t.Fatalf("query payload: %v", err)
	}
	if raw != "{}" {
		t.Errorf("nil Payload landed as %q, want %q", raw, "{}")
	}
}
