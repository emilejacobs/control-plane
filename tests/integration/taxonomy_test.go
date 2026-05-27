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

// TestTaxonomyUpsertSitePersistsBrandMetadata locks ADR-033 § 4: Brand
// is captured per Site as flat metadata (brand_name + brand_external_id)
// rather than as its own table or hierarchy level. UpsertSite ties the
// site to its parent client's local UUID, stamps the brand columns, and
// returns the site's local UUID. A second observation with the same
// external_id updates name + brand metadata + last_synced_at, reactivates,
// and keeps the local UUID stable (devices.site_id reference).
func TestTaxonomyUpsertSitePersistsBrandMetadata(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := taxonomy.NewStore(pool)

	syncedAt := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	clientID, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "client-ext-1",
		Name:       "Rao Holdings",
		SyncedAt:   syncedAt,
	})
	if err != nil {
		t.Fatalf("UpsertClient: %v", err)
	}

	siteID, err := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID:      "site-ext-42",
		Name:            "Rao Mesa AZ",
		ClientID:        clientID,
		BrandName:       "Burger King",
		BrandExternalID: "brand-ext-bk",
		SyncedAt:        syncedAt,
	})
	if err != nil {
		t.Fatalf("UpsertSite: %v", err)
	}

	var (
		name, gotClientID, brandName, brandExtID string
		active                                   bool
		gotSyncedAt                              time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT name, client_id::text, brand_name, brand_external_id, active, last_synced_at
		FROM sites WHERE external_id = $1
	`, "site-ext-42").Scan(&name, &gotClientID, &brandName, &brandExtID, &active, &gotSyncedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "Rao Mesa AZ" {
		t.Errorf("name: got %q", name)
	}
	if gotClientID != clientID {
		t.Errorf("client_id: got %s want %s", gotClientID, clientID)
	}
	if brandName != "Burger King" || brandExtID != "brand-ext-bk" {
		t.Errorf("brand metadata: got (%q,%q)", brandName, brandExtID)
	}
	if !active {
		t.Errorf("active: false")
	}
	if !gotSyncedAt.Equal(syncedAt) {
		t.Errorf("last_synced_at: got %v want %v", gotSyncedAt, syncedAt)
	}

	// Simulate a previous sweep deactivating the site, then re-observe with
	// a new brand label (Rao moved the store to a different brand). UUID
	// must be stable; active must flip back to true.
	if _, err := pool.Exec(ctx, `UPDATE sites SET active = false WHERE external_id = $1`, "site-ext-42"); err != nil {
		t.Fatal(err)
	}
	next := syncedAt.Add(24 * time.Hour)
	id2, err := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID:      "site-ext-42",
		Name:            "Rao Mesa AZ",
		ClientID:        clientID,
		BrandName:       "Dunkin Donuts",
		BrandExternalID: "brand-ext-dd",
		SyncedAt:        next,
	})
	if err != nil {
		t.Fatalf("second UpsertSite: %v", err)
	}
	if id2 != siteID {
		t.Errorf("UUID changed: %s vs %s (orphans devices.site_id)", siteID, id2)
	}
	if err := pool.QueryRow(ctx, `
		SELECT brand_name, brand_external_id, active FROM sites WHERE external_id = $1
	`, "site-ext-42").Scan(&brandName, &brandExtID, &active); err != nil {
		t.Fatal(err)
	}
	if brandName != "Dunkin Donuts" || brandExtID != "brand-ext-dd" || !active {
		t.Errorf("after re-observe: brand=(%q,%q) active=%v", brandName, brandExtID, active)
	}
}

// TestTaxonomySweepInactiveMarksAbsentRows locks ADR-033 § 5 absent-row
// detection: after a sync run finishes, any client or site row whose
// last_synced_at predates the run's start time is the upstream's
// "absent" signal — the syncer never re-touched it. SweepInactive flips
// active=false for those rows. Rows touched during the run (their
// last_synced_at >= cutoff) stay active. The local id is preserved so
// downstream foreign-key references (devices.site_id, operator_sites)
// still resolve and operators see an "Inactive" badge rather than a
// disappeared site.
func TestTaxonomySweepInactiveMarksAbsentRows(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := taxonomy.NewStore(pool)

	old := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	current := cutoff.Add(5 * time.Minute)

	// Two clients from a previous run.
	staleClientID, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "client-stale", Name: "Stale", SyncedAt: old,
	})
	if err != nil {
		t.Fatal(err)
	}
	freshClientID, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "client-fresh", Name: "Fresh", SyncedAt: old,
	})
	if err != nil {
		t.Fatal(err)
	}
	// One site under each client from a previous run.
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "site-stale", Name: "Stale Site", ClientID: staleClientID,
		BrandName: "BK", BrandExternalID: "bk", SyncedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "site-fresh", Name: "Fresh Site", ClientID: freshClientID,
		BrandName: "BK", BrandExternalID: "bk", SyncedAt: old,
	}); err != nil {
		t.Fatal(err)
	}

	// Current run re-touches the "fresh" rows only.
	if _, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "client-fresh", Name: "Fresh", SyncedAt: current,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "site-fresh", Name: "Fresh Site", ClientID: freshClientID,
		BrandName: "BK", BrandExternalID: "bk", SyncedAt: current,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.SweepInactive(ctx, cutoff); err != nil {
		t.Fatalf("SweepInactive: %v", err)
	}

	check := func(table string) map[string]bool {
		t.Helper()
		rs, err := pool.Query(ctx,
			`SELECT external_id, active FROM `+table)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		out := map[string]bool{}
		for rs.Next() {
			var ext string
			var active bool
			if err := rs.Scan(&ext, &active); err != nil {
				t.Fatal(err)
			}
			out[ext] = active
		}
		return out
	}

	if got := check("clients"); got["client-fresh"] != true || got["client-stale"] != false {
		t.Errorf("clients active: got %+v want {client-fresh:true client-stale:false}", got)
	}
	if got := check("sites"); got["site-fresh"] != true || got["site-stale"] != false {
		t.Errorf("sites active: got %+v want {site-fresh:true site-stale:false}", got)
	}
}
