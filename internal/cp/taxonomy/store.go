// Package taxonomy mirrors the upstream clients/sites HTTP API
// (api.uknomi.com) into CP's local Postgres so the dashboard, pickers,
// and authz all read locally. ADR-033 locks the architecture; this
// package owns the persistence layer + the upstream HTTP client; the
// orchestration shell lives in cmd/taxonomy-sync.
package taxonomy

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists clients and sites mirrored from the upstream API.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore binds a pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ClientRow is the per-client payload the syncer hands to UpsertClient.
// ExternalID is the upstream primary key; Name is the display label;
// SyncedAt is the sync run's start time, written into last_synced_at so
// the post-sync sweep can tell present-this-run from absent-this-run.
type ClientRow struct {
	ExternalID string
	Name       string
	SyncedAt   time.Time
}

// UpsertClient inserts or updates a client row keyed by external_id and
// returns its local UUID. New rows default to active=true; the syncer
// flips active later for rows that arrive flagged inactive or that no
// longer appear in a sync.
func (s *Store) UpsertClient(ctx context.Context, in ClientRow) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO clients (external_id, name, last_synced_at)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, in.ExternalID, in.Name, in.SyncedAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert client %q: %w", in.ExternalID, err)
	}
	return id, nil
}
