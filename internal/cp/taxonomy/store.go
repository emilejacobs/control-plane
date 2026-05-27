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

// SiteRow is the per-site payload the syncer hands to UpsertSite. The
// brand columns are flat metadata: CP does not model Brand as its own
// hierarchy level (ADR-033 § 4). ClientID is the parent's local UUID,
// returned from a prior UpsertClient call.
type SiteRow struct {
	ExternalID      string
	Name            string
	ClientID        string
	BrandName       string
	BrandExternalID string
	SyncedAt        time.Time
}

// UpsertClient inserts or updates a client row keyed by external_id and
// returns its local UUID. On conflict the row's name and last_synced_at
// are refreshed and active is flipped back to true (reactivation: a
// sweep may have parked the row inactive, and re-observing it upstream
// is the explicit "still here" signal). The local UUID is preserved —
// devices.site_id and operator_sites grants reference local IDs, so
// reissuing them would orphan every assignment.
func (s *Store) UpsertClient(ctx context.Context, in ClientRow) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO clients (external_id, name, last_synced_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (external_id) DO UPDATE
		   SET name           = EXCLUDED.name,
		       last_synced_at = EXCLUDED.last_synced_at,
		       active         = true
		RETURNING id::text
	`, in.ExternalID, in.Name, in.SyncedAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert client %q: %w", in.ExternalID, err)
	}
	return id, nil
}

// UpsertSite inserts or updates a site row keyed by external_id and
// returns its local UUID. On conflict the row's name, client linkage,
// brand metadata, and last_synced_at are refreshed and active is flipped
// back to true. The local UUID is preserved — devices.site_id and
// operator_sites grants point at it.
func (s *Store) UpsertSite(ctx context.Context, in SiteRow) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sites (external_id, name, client_id, brand_name, brand_external_id, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (external_id) DO UPDATE
		   SET name              = EXCLUDED.name,
		       client_id         = EXCLUDED.client_id,
		       brand_name        = EXCLUDED.brand_name,
		       brand_external_id = EXCLUDED.brand_external_id,
		       last_synced_at    = EXCLUDED.last_synced_at,
		       active            = true
		RETURNING id::text
	`, in.ExternalID, in.Name, in.ClientID, in.BrandName, in.BrandExternalID, in.SyncedAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert site %q: %w", in.ExternalID, err)
	}
	return id, nil
}

// SweepInactive flips active=false on every clients and sites row whose
// last_synced_at predates cutoff — those rows the just-finished sync run
// never re-touched, which is the upstream's "absent" signal per
// ADR-033 § 5. Rows are not hard-deleted: devices.site_id and
// operator_sites grants reference local UUIDs, and the dashboard renders
// inactive entities with an "Inactive" badge instead of silent
// disappearance. Reactivation happens for free in the next UpsertClient
// / UpsertSite that re-observes the external_id.
//
// Cutoff must be the sync run's start time (captured before any
// network call), not "now" — using "now" would race with rows the run
// upserted only seconds earlier.
func (s *Store) SweepInactive(ctx context.Context, cutoff time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE clients SET active = false
		 WHERE last_synced_at IS NULL OR last_synced_at < $1
	`, cutoff); err != nil {
		return fmt.Errorf("sweep clients: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE sites SET active = false
		 WHERE last_synced_at IS NULL OR last_synced_at < $1
	`, cutoff); err != nil {
		return fmt.Errorf("sweep sites: %w", err)
	}
	return nil
}

// StatusSnapshot is the read surface behind GET /taxonomy/status. The
// counts let the Settings page render "N clients, M sites (M active)";
// LastSyncedAt is the most recent observation across either table (nil
// when nothing has synced yet, which the dashboard renders as "Never").
type StatusSnapshot struct {
	ClientsTotal  int
	ClientsActive int
	SitesTotal    int
	SitesActive   int
	LastSyncedAt  *time.Time
}

// Status returns the counts and most recent last_synced_at across
// clients + sites in a single read. LastSyncedAt is nil when no row
// has ever synced.
func (s *Store) Status(ctx context.Context) (StatusSnapshot, error) {
	var snap StatusSnapshot
	var last *time.Time
	if err := s.pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM clients),
		    (SELECT count(*) FROM clients WHERE active),
		    (SELECT count(*) FROM sites),
		    (SELECT count(*) FROM sites WHERE active),
		    GREATEST(
		        (SELECT max(last_synced_at) FROM clients),
		        (SELECT max(last_synced_at) FROM sites)
		    )
	`).Scan(&snap.ClientsTotal, &snap.ClientsActive, &snap.SitesTotal, &snap.SitesActive, &last); err != nil {
		return StatusSnapshot{}, fmt.Errorf("status: %w", err)
	}
	snap.LastSyncedAt = last
	return snap, nil
}
