package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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

// TestAuditLogEndToEnd is the Issue 20 AC: a known sequence of requests
// (first-run, failed login, successful login) lands as four audit_log
// rows joinable by the X-Correlation-Id the caller threads through. Each
// row carries the correct action, outcome, source_ip, and the operator
// email in payload. Together they form the audit timeline an operator
// sees for "the bootstrap + one wrong attempt + one successful login"
// scenario in PRD § User Story 5.
func TestAuditLogEndToEnd(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const (
		email      = "audit-log-test@acmecorp.test"
		password   = "correct-horse-battery-staple"
		firstRunCI = "corr-audit-firstrun"
		failedCI   = "corr-audit-bad-login"
		successCI  = "corr-audit-good-login"
	)

	// 1) Bootstrap the first operator.
	if code := doAuthCall(t, srv.URL, "/auth/first-run", map[string]any{
		"email": email, "password": password,
	}, "11111111-0000-0000-0000-000000000001", firstRunCI); code != http.StatusCreated {
		t.Fatalf("first-run: got %d want 201", code)
	}

	// 2) A wrong-password attempt.
	if code := doAuthCall(t, srv.URL, "/auth/login", map[string]any{
		"email": email, "password": "WRONG", "totp_code": "000000",
	}, "11111111-0000-0000-0000-000000000002", failedCI); code != http.StatusUnauthorized {
		t.Fatalf("bad-login: got %d want 401", code)
	}

	// 3) A correct-credentials attempt (operator has no TOTP enrolled yet,
	//    so login still succeeds — TOTP-required is a separate gate).
	if code := doAuthCall(t, srv.URL, "/auth/login", map[string]any{
		"email": email, "password": password, "totp_code": "000000",
	}, "11111111-0000-0000-0000-000000000003", successCI); code != http.StatusOK {
		t.Fatalf("good-login: got %d want 200", code)
	}

	// audit_log should now carry one row per call, joinable by
	// correlation_id and ordered by `at`.
	type row struct {
		action, outcome, sourceIP, correlationID string
		payload                                  map[string]any
	}
	want := []struct {
		corrID  string
		action  string
		outcome string
	}{
		{firstRunCI, "audit.first_run", "success"},
		{failedCI, "audit.login", "failure"},
		{successCI, "audit.login", "success"},
	}
	for _, w := range want {
		var got row
		err := srv.Pool.QueryRow(ctx, `
			SELECT action, outcome, source_ip, correlation_id, payload
			FROM audit_log WHERE correlation_id = $1
		`, w.corrID).Scan(&got.action, &got.outcome, &got.sourceIP, &got.correlationID, &got.payload)
		if err != nil {
			t.Errorf("query audit_log for %q: %v", w.corrID, err)
			continue
		}
		if got.action != w.action {
			t.Errorf("%s: action got %q want %q", w.corrID, got.action, w.action)
		}
		if got.outcome != w.outcome {
			t.Errorf("%s: outcome got %q want %q", w.corrID, got.outcome, w.outcome)
		}
		if got.sourceIP == "" {
			t.Errorf("%s: source_ip is empty", w.corrID)
		}
		if got.payload["email"] != email {
			t.Errorf("%s: payload[email] got %v want %q", w.corrID, got.payload["email"], email)
		}
	}

	// The failed-login row carries the "invalid_credentials" reason — the
	// key triage signal for a brute-force pattern.
	var failedReason string
	err := srv.Pool.QueryRow(ctx, `
		SELECT payload->>'reason' FROM audit_log WHERE correlation_id = $1
	`, failedCI).Scan(&failedReason)
	if err != nil {
		t.Fatalf("query failed reason: %v", err)
	}
	if failedReason != "invalid_credentials" {
		t.Errorf("failed-login reason: got %q want %q", failedReason, "invalid_credentials")
	}

	// Read-only requests must NOT inflate audit_log — assert that the
	// total row count equals the number of POSTs we made. (Three POSTs;
	// HTTPMiddleware suppresses its envelope on every one because each
	// handler audited explicitly.)
	var total int
	if err := srv.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&total); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	if total != 3 {
		t.Errorf("audit_log rows: got %d want 3; the handler-suppresses-middleware contract is broken", total)
	}
}

// doAuthCall posts JSON to baseURL+path with the given Idempotency-Key
// and X-Correlation-Id headers and returns the status code.
func doAuthCall(t *testing.T, baseURL, path string, body map[string]any, idemKey, corrID string) int {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("X-Correlation-Id", corrID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
