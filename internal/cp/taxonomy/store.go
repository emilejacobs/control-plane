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

// syncLockKey is the Postgres advisory-lock key the taxonomy sync
// uses to serialize itself (ADR-033 § 8). The bytes spell "txnsync"
// in ASCII — globally distinct from any other CP advisory lock.
const syncLockKey int64 = 0x74786E73796E63

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
// returned from a prior UpsertClient call. Active mirrors the
// upstream's per-store active flag — ADR-033 § 5's "API flag" signal.
// Absent-from-sync rows are handled separately via SweepInactive.
type SiteRow struct {
	ExternalID      string
	Name            string
	ClientID        string
	BrandName       string
	BrandExternalID string
	Active          bool
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
		INSERT INTO sites (external_id, name, client_id, brand_name, brand_external_id, active, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (external_id) DO UPDATE
		   SET name              = EXCLUDED.name,
		       client_id         = EXCLUDED.client_id,
		       brand_name        = EXCLUDED.brand_name,
		       brand_external_id = EXCLUDED.brand_external_id,
		       active            = EXCLUDED.active,
		       last_synced_at    = EXCLUDED.last_synced_at
		RETURNING id::text
	`, in.ExternalID, in.Name, in.ClientID, in.BrandName, in.BrandExternalID, in.Active, in.SyncedAt).Scan(&id)
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

// ClientWithSites is one row of the picker tree: a client and its sites
// in display order (sites sorted by name).
type ClientWithSites struct {
	ID         string
	ExternalID string
	Name       string
	Sites      []SiteSummary
}

// SiteSummary is the on-the-picker representation of a Site. Carries
// enough to render a label ("BK Mesa — Burger King · 50") and apply
// the assignment (ID is the local FK target for devices.site_id).
type SiteSummary struct {
	ID              string
	ExternalID      string
	Name            string
	BrandName       string
	BrandExternalID string
	Active          bool
}

// ListClientsWithSites returns the picker tree. By default only active
// clients × active sites are included; includeInactive=true returns
// all rows (the staff fallback for re-assigning a device whose
// previous site was incorrectly swept). Clients with zero matching
// sites are excluded — empty groups are useless in the picker.
//
// Ordering: clients by name, sites within each client by name. The
// picker shows them in this order so the operator sees the same
// layout every time.
func (s *Store) ListClientsWithSites(ctx context.Context, includeInactive bool) ([]ClientWithSites, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
		    c.id::text, c.external_id, c.name,
		    s.id::text, s.external_id, s.name,
		    s.brand_name, s.brand_external_id, s.active
		FROM clients c
		INNER JOIN sites s ON s.client_id = c.id
		WHERE ($1 OR c.active)
		  AND ($1 OR s.active)
		ORDER BY c.name, s.name
	`, includeInactive)
	if err != nil {
		return nil, fmt.Errorf("list clients with sites: %w", err)
	}
	defer rows.Close()

	// Build the tree in pass order: rows arrive grouped by client because of ORDER BY c.name.
	var out []ClientWithSites
	var currentID string
	for rows.Next() {
		var (
			cID, cExt, cName        string
			sID, sExt, sName        string
			brandName, brandExtID   *string
			siteActive              bool
		)
		if err := rows.Scan(&cID, &cExt, &cName, &sID, &sExt, &sName, &brandName, &brandExtID, &siteActive); err != nil {
			return nil, fmt.Errorf("scan site row: %w", err)
		}
		if cID != currentID {
			out = append(out, ClientWithSites{ID: cID, ExternalID: cExt, Name: cName})
			currentID = cID
		}
		out[len(out)-1].Sites = append(out[len(out)-1].Sites, SiteSummary{
			ID:              sID,
			ExternalID:      sExt,
			Name:            sName,
			BrandName:       strOrEmpty(brandName),
			BrandExternalID: strOrEmpty(brandExtID),
			Active:          siteActive,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter site rows: %w", err)
	}
	return out, nil
}

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// AcquireSyncLock tries to acquire the taxonomy-sync advisory lock on
// a dedicated connection held for the duration of the run. Returns
// ok=true and a release closure if obtained; ok=false (with a no-op
// release) when another invocation already holds it — the caller
// logs and exits gracefully per ADR-033 § 8 (no queueing). Release
// runs pg_advisory_unlock and returns the connection to the pool;
// it is safe to defer immediately after AcquireSyncLock returns.
//
// Postgres releases session-level advisory locks automatically when
// the underlying connection drops — a crashed sync task therefore
// never wedges the lock for the next scheduled run.
func (s *Store) AcquireSyncLock(ctx context.Context) (release func(), ok bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return func() {}, false, fmt.Errorf("acquire conn: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock($1)`, syncLockKey).Scan(&got); err != nil {
		conn.Release()
		return func() {}, false, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}
	if !got {
		conn.Release()
		return func() {}, false, nil
	}
	return func() {
		// Use a fresh background context so the unlock still runs if
		// the original ctx was cancelled (e.g. SIGTERM mid-run).
		_, _ = conn.Exec(context.Background(),
			`SELECT pg_advisory_unlock($1)`, syncLockKey)
		conn.Release()
	}, true, nil
}
