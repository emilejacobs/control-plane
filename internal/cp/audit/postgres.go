package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

// PostgresWriter persists audit Entries to the audit_log table created by
// migration 010_audit_log.sql. Production wires this; tests against a
// real database use it through the same Writer seam as MemoryWriter.
type PostgresWriter struct {
	pool *pgxpool.Pool
}

// NewPostgresWriter binds a Writer to the given pool.
func NewPostgresWriter(pool *pgxpool.Pool) *PostgresWriter {
	return &PostgresWriter{pool: pool}
}

// Write inserts one audit_log row, emits the slog co-emission, and marks
// the HTTPMiddleware tracker so the middleware envelope is suppressed.
// A write that fails is propagated to the caller, but in practice every
// call site does `_ = w.Write(...)` — losing a single audit row should
// never break the user-facing request. A future Phase-1 hardening pass
// can add CloudWatch alarming on insert failures.
func (p *PostgresWriter) Write(ctx context.Context, e Entry) error {
	e.CorrelationID = cplog.CorrelationIDFromContext(ctx)
	emitSlog(ctx, e)
	markHandled(ctx)

	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if len(payload) == 0 || string(payload) == "null" {
		payload = []byte("{}")
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO audit_log
		    (action, actor_id, actor_type, resource_kind, resource_id,
		     correlation_id, source_ip, user_agent, outcome, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		e.Action, e.ActorID, string(e.ActorType), e.ResourceKind, e.ResourceID,
		e.CorrelationID, e.SourceIP, e.UserAgent, e.Outcome, payload,
	)
	if err != nil {
		return fmt.Errorf("insert audit_log: %w", err)
	}
	return nil
}
