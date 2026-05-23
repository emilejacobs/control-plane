// Package audit is the Control Plane's append-only audit-event sink. Per
// PRD § audit_log, every state-changing endpoint and every security-relevant
// ingest event lands here as a row in the audit_log Postgres table, with
// the same correlation_id the cplog middleware threads through the request.
//
// audit.Writer is the seam: production wires a PostgresWriter; tests wire a
// MemoryWriter through the same interface.
package audit

import (
	"context"
	"sync"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

// ActorType names the kind of identity that issued an audited action.
type ActorType string

const (
	ActorOperator ActorType = "operator"
	ActorAgent    ActorType = "agent"
	ActorSystem   ActorType = "system"
)

// Entry is one audit event. The fields mirror the audit_log schema; Writer
// implementations stamp at + correlation_id automatically (the at column
// defaults to now() in the table; correlation_id comes from cplog context).
type Entry struct {
	Action       string
	ActorID      string
	ActorType    ActorType
	ResourceKind string
	ResourceID   string
	SourceIP     string
	UserAgent    string
	Outcome      string
	Payload      map[string]any

	// CorrelationID is populated by the Writer from cplog context; tests
	// that bypass the Writer interface (rare) can set it directly.
	CorrelationID string
}

// Writer commits one Entry. Implementations must be safe for concurrent use.
type Writer interface {
	Write(ctx context.Context, e Entry) error
}

// MemoryWriter records Entries in-memory for tests. The zero value is ready.
type MemoryWriter struct {
	mu      sync.Mutex
	entries []Entry
}

// Write stamps the correlation_id from cplog context and appends the entry.
func (m *MemoryWriter) Write(ctx context.Context, e Entry) error {
	e.CorrelationID = cplog.CorrelationIDFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

// Entries returns a copy of every Entry written so far, in write order.
func (m *MemoryWriter) Entries() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, len(m.entries))
	copy(out, m.entries)
	return out
}
