package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/storage"
	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

// TestTaxonomyUpsertClientInsertsActiveRow is the tracer bullet for #18:
// a brand-new client_external_id is inserted as an active row with the
// supplied name and last_synced_at stamped to "now". Drives migration
// 019 into existence.
func TestTaxonomyUpsertClientInsertsActiveRow(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := taxonomy.NewStore(pool)
	syncedAt := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	if _, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "client-ext-1",
		Name:       "Acme Corp",
		SyncedAt:   syncedAt,
	}); err != nil {
		t.Fatalf("UpsertClient: %v", err)
	}

	var name string
	var active bool
	var lastSyncedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT name, active, last_synced_at
		FROM clients
		WHERE external_id = $1
	`, "client-ext-1").Scan(&name, &active, &lastSyncedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "Acme Corp" {
		t.Errorf("name: got %q want %q", name, "Acme Corp")
	}
	if !active {
		t.Errorf("active: got false want true")
	}
	if !lastSyncedAt.Equal(syncedAt) {
		t.Errorf("last_synced_at: got %v want %v", lastSyncedAt, syncedAt)
	}
}

// TestTaxonomyUpsertClientIsIdempotent locks ADR-033 § 5 reactivation
// semantics: a second sync that re-observes a previously-inactive client
// must update the row's name + last_synced_at and flip active back to
// true without touching the local UUID. The local UUID is the foreign
// key target for sites and operator_sites — re-issuing it would orphan
// every grant.
func TestTaxonomyUpsertClientIsIdempotent(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := taxonomy.NewStore(pool)

	first := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	idFirst, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "client-ext-1",
		Name:       "Acme Corp",
		SyncedAt:   first,
	})
	if err != nil {
		t.Fatalf("first UpsertClient: %v", err)
	}

	// Simulate a previous sweep that deactivated the row.
	if _, err := pool.Exec(ctx,
		`UPDATE clients SET active = false WHERE external_id = $1`,
		"client-ext-1"); err != nil {
		t.Fatalf("simulate prior sweep: %v", err)
	}

	second := first.Add(24 * time.Hour)
	idSecond, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "client-ext-1",
		Name:       "Acme Corporation", // rename upstream
		SyncedAt:   second,
	})
	if err != nil {
		t.Fatalf("second UpsertClient: %v", err)
	}
	if idSecond != idFirst {
		t.Errorf("UUID changed: first=%s second=%s — orphans every device.site_id and operator_sites grant", idFirst, idSecond)
	}

	var name string
	var active bool
	var lastSyncedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT name, active, last_synced_at FROM clients WHERE external_id = $1
	`, "client-ext-1").Scan(&name, &active, &lastSyncedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "Acme Corporation" {
		t.Errorf("name not updated: got %q want %q", name, "Acme Corporation")
	}
	if !active {
		t.Errorf("active not reactivated: got false want true")
	}
	if !lastSyncedAt.Equal(second) {
		t.Errorf("last_synced_at: got %v want %v", lastSyncedAt, second)
	}

	// And only one row exists for that external_id.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM clients WHERE external_id = $1`, "client-ext-1").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("row count: got %d want 1 (upsert must not duplicate)", count)
	}
}
