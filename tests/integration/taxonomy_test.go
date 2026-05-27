package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// TestTaxonomyStatusCounts locks the read surface behind
// GET /taxonomy/status (ADR-033 § 8): the Settings page renders "Last
// successful sync: 4h ago — N clients, M sites (M active)". Status
// computes the four counts plus the most recent last_synced_at across
// either table, all from a single store call so the handler is a thin
// pass-through.
func TestTaxonomyStatusCounts(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := taxonomy.NewStore(pool)

	earlier := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	later := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	// 2 clients (1 active, 1 inactive) and 3 sites (2 active, 1 inactive).
	cActive, _ := store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "c1", Name: "Active", SyncedAt: later})
	cInactive, _ := store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "c2", Name: "Gone", SyncedAt: earlier})
	if _, err := pool.Exec(ctx, `UPDATE clients SET active = false WHERE external_id = $1`, "c2"); err != nil {
		t.Fatal(err)
	}
	_ = cInactive
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{ExternalID: "s1", Name: "S1", ClientID: cActive, BrandName: "BK", BrandExternalID: "bk", SyncedAt: later}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{ExternalID: "s2", Name: "S2", ClientID: cActive, BrandName: "BK", BrandExternalID: "bk", SyncedAt: earlier}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{ExternalID: "s3", Name: "S3", ClientID: cActive, BrandName: "BK", BrandExternalID: "bk", SyncedAt: earlier}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sites SET active = false WHERE external_id = $1`, "s3"); err != nil {
		t.Fatal(err)
	}

	got, err := store.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.ClientsTotal != 2 {
		t.Errorf("ClientsTotal: got %d want 2", got.ClientsTotal)
	}
	if got.ClientsActive != 1 {
		t.Errorf("ClientsActive: got %d want 1", got.ClientsActive)
	}
	if got.SitesTotal != 3 {
		t.Errorf("SitesTotal: got %d want 3", got.SitesTotal)
	}
	if got.SitesActive != 2 {
		t.Errorf("SitesActive: got %d want 2", got.SitesActive)
	}
	if got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(later) {
		t.Errorf("LastSyncedAt: got %v want %v", got.LastSyncedAt, later)
	}
}

// TestTaxonomyStatusEmptyReportsNilLastSyncedAt covers the
// never-run-yet state: the dashboard renders "Never" rather than a
// 1970 epoch when there is no synced row at all.
func TestTaxonomyStatusEmptyReportsNilLastSyncedAt(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := taxonomy.NewStore(pool)

	got, err := store.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.ClientsTotal != 0 || got.SitesTotal != 0 {
		t.Errorf("counts: got %+v want all zero", got)
	}
	if got.LastSyncedAt != nil {
		t.Errorf("LastSyncedAt: got %v want nil", got.LastSyncedAt)
	}
}

// TestTaxonomyRunnerOneBrandOneStore is the cycle-8 tracer for the
// orchestration shell: given a httptest fake of the upstream API
// returning a single active brand whose only store carries one client,
// the Runner walks /brand → /brand/{id}/store, upserts the client first
// (its UUID is needed by the site), then upserts the site with the
// brand stamped onto the row.
func TestTaxonomyRunnerOneBrandOneStore(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/signin":
			_, _ = w.Write([]byte(`{"token":"jwt"}`))
		case "/brand":
			_, _ = w.Write([]byte(`[{"id":"bk","name":"Burger King","active":true}]`))
		case "/brand/bk/store":
			_, _ = w.Write([]byte(`[{"id":"s1","name":"Mesa AZ","active":true,"client":{"id":"rao","name":"Rao"}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	runner := taxonomy.NewRunner(
		taxonomy.NewClient(srv.URL, "u", "p"),
		taxonomy.NewStore(pool),
		func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) },
	)
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var clientName, brandName, brandExtID string
	var active bool
	if err := pool.QueryRow(ctx,
		`SELECT name, active FROM clients WHERE external_id = $1`, "rao",
	).Scan(&clientName, &active); err != nil {
		t.Fatalf("read client: %v", err)
	}
	if clientName != "Rao" || !active {
		t.Errorf("client row: name=%q active=%v", clientName, active)
	}
	if err := pool.QueryRow(ctx,
		`SELECT brand_name, brand_external_id, active FROM sites WHERE external_id = $1`, "s1",
	).Scan(&brandName, &brandExtID, &active); err != nil {
		t.Fatalf("read site: %v", err)
	}
	if brandName != "Burger King" || brandExtID != "bk" || !active {
		t.Errorf("site row: brand=(%q,%q) active=%v", brandName, brandExtID, active)
	}
}

// TestTaxonomyRunnerDedupesClientAcrossBrands locks ADR-033 § 4: a
// single client (Rao) that operates both Burger King and Dunkin Donuts
// shows up nested in stores under two different brands. CP's hierarchy
// is Client → Site with Brand as flat per-Site metadata, so the run
// produces exactly one clients row, two sites rows, each stamped with
// its own brand label.
func TestTaxonomyRunnerDedupesClientAcrossBrands(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/signin":
			_, _ = w.Write([]byte(`{"token":"jwt"}`))
		case "/brand":
			_, _ = w.Write([]byte(`[
				{"id":"bk","name":"Burger King","active":true},
				{"id":"dd","name":"Dunkin Donuts","active":true}
			]`))
		case "/brand/bk/store":
			_, _ = w.Write([]byte(`[{"id":"s-bk-1","name":"Rao BK Mesa","active":true,"client":{"id":"rao","name":"Rao Holdings"}}]`))
		case "/brand/dd/store":
			_, _ = w.Write([]byte(`[{"id":"s-dd-1","name":"Rao DD Tempe","active":true,"client":{"id":"rao","name":"Rao Holdings"}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	runner := taxonomy.NewRunner(
		taxonomy.NewClient(srv.URL, "u", "p"),
		taxonomy.NewStore(pool),
		func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) },
	)
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var clientCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM clients WHERE external_id = $1`, "rao").Scan(&clientCount); err != nil {
		t.Fatal(err)
	}
	if clientCount != 1 {
		t.Errorf("client rows for rao: got %d want 1 (must dedupe across brands)", clientCount)
	}

	// Sites split by brand, both stamped with their own brand metadata.
	rows, err := pool.Query(ctx, `SELECT external_id, brand_external_id FROM sites ORDER BY external_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var ext, brand string
		if err := rows.Scan(&ext, &brand); err != nil {
			t.Fatal(err)
		}
		got[ext] = brand
	}
	want := map[string]string{"s-bk-1": "bk", "s-dd-1": "dd"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("site %s: brand=%q want %q (full got=%+v)", k, got[k], v, got)
		}
	}

	// Both sites point at the same local client_id.
	rows2, err := pool.Query(ctx, `SELECT DISTINCT client_id::text FROM sites WHERE external_id IN ('s-bk-1','s-dd-1')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows2.Close()
	var clientIDs []string
	for rows2.Next() {
		var s string
		if err := rows2.Scan(&s); err != nil {
			t.Fatal(err)
		}
		clientIDs = append(clientIDs, s)
	}
	if len(clientIDs) != 1 {
		t.Errorf("distinct client_id across both sites: got %v want one shared id", clientIDs)
	}
}
