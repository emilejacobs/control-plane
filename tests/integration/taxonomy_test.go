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
		Active:          true,
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
		Active:          true,
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
		BrandName: "BK", BrandExternalID: "bk", Active: true, SyncedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "site-fresh", Name: "Fresh Site", ClientID: freshClientID,
		BrandName: "BK", BrandExternalID: "bk", Active: true, SyncedAt: old,
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
		BrandName: "BK", BrandExternalID: "bk", Active: true, SyncedAt: current,
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
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{ExternalID: "s1", Name: "S1", ClientID: cActive, BrandName: "BK", BrandExternalID: "bk", Active: true, SyncedAt: later}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{ExternalID: "s2", Name: "S2", ClientID: cActive, BrandName: "BK", BrandExternalID: "bk", Active: true, SyncedAt: earlier}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{ExternalID: "s3", Name: "S3", ClientID: cActive, BrandName: "BK", BrandExternalID: "bk", Active: true, SyncedAt: earlier}); err != nil {
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

// taxoSigninJSON is the Cognito-native InitiateAuth shape the real
// api.uknomi.com /user/signin returns (verified 2026-05-27). Fixtures
// use it so tests stay faithful to the wire shape.
const taxoSigninJSON = `{"AuthenticationResult":{"IdToken":"jwt","AccessToken":"acc","RefreshToken":"r","ExpiresIn":86400,"TokenType":"Bearer"}}`

// TestTaxonomyRunnerOneBrandOneStore is the tracer for the orchestration
// shell against the real upstream wire shape: numeric ids on /brand and
// /brand/{id}/store, flat client_id (no nested client), no `active`
// field. Runner walks /brand → /brand/{id}/store, derives the client
// name from the joined set of brands the client operates (the
// brand-as-client-name substitute until the upstream exposes real
// client metadata — #18 follow-up), upserts the client, then stamps
// the brand on the site row.
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
			_, _ = w.Write([]byte(taxoSigninJSON))
		case "/brand":
			_, _ = w.Write([]byte(`[{"id":12,"name":"Burger King"}]`))
		case "/brand/12/store":
			_, _ = w.Write([]byte(`[{"id":50,"name":"Mesa AZ","client_id":14,"brand_id":12}]`))
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
		`SELECT name, active FROM clients WHERE external_id = $1`, "14",
	).Scan(&clientName, &active); err != nil {
		t.Fatalf("read client: %v", err)
	}
	if clientName != "Burger King" || !active {
		t.Errorf("client row: name=%q active=%v (want brand-derived 'Burger King' + active=true)", clientName, active)
	}
	if err := pool.QueryRow(ctx,
		`SELECT brand_name, brand_external_id, active FROM sites WHERE external_id = $1`, "50",
	).Scan(&brandName, &brandExtID, &active); err != nil {
		t.Fatalf("read site: %v", err)
	}
	if brandName != "Burger King" || brandExtID != "12" || !active {
		t.Errorf("site row: brand=(%q,%q) active=%v", brandName, brandExtID, active)
	}
}

// TestTaxonomyRunnerDedupesClientAcrossBrands locks ADR-033 § 4 against
// the real wire shape: client 14 operates Burger King (brand 12) AND
// Dunkin Donuts (brand 13) in the live data; both /brand/12/store and
// /brand/13/store return a store with client_id=14. CP's hierarchy is
// Client → Site with Brand as flat per-Site metadata, so the run
// produces exactly one clients row and two sites rows, each stamped
// with its own brand label.
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
			_, _ = w.Write([]byte(taxoSigninJSON))
		case "/brand":
			_, _ = w.Write([]byte(`[
				{"id":12,"name":"Burger King"},
				{"id":13,"name":"Dunkin Donuts"}
			]`))
		case "/brand/12/store":
			_, _ = w.Write([]byte(`[{"id":60,"name":"BK Mesa","client_id":14,"brand_id":12}]`))
		case "/brand/13/store":
			_, _ = w.Write([]byte(`[{"id":50,"name":"DD09","client_id":14,"brand_id":13}]`))
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM clients WHERE external_id = $1`, "14").Scan(&clientCount); err != nil {
		t.Fatal(err)
	}
	if clientCount != 1 {
		t.Errorf("client rows for client_id=14: got %d want 1 (must dedupe across brands)", clientCount)
	}

	// Multi-brand client gets the joined, sorted brand list as their
	// name — the brand-as-client-name substitute.
	var clientName string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM clients WHERE external_id = $1`, "14",
	).Scan(&clientName); err != nil {
		t.Fatal(err)
	}
	if clientName != "Burger King, Dunkin Donuts" {
		t.Errorf("client name: got %q want %q (sorted comma-joined brand names)",
			clientName, "Burger King, Dunkin Donuts")
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
	want := map[string]string{"50": "13", "60": "12"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("site %s: brand=%q want %q (full got=%+v)", k, got[k], v, got)
		}
	}

	// Both sites point at the same local client_id.
	rows2, err := pool.Query(ctx, `SELECT DISTINCT client_id::text FROM sites WHERE external_id IN ('50','60')`)
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

// TestTaxonomyRunnerSweepsAbsentRows locks ADR-033 § 5 absence-detection
// against the real wire shape: a previously-mirrored client + site that
// the current upstream payload omits entirely gets active=false via
// the post-walk sweep. The upstream exposes no per-row `active` flag,
// so absence-from-walk is the sole soft-delete signal. (The Store
// layer's Active field is still tested directly via
// TestTaxonomyUpsertSitePersistsBrandMetadata — the per-row flag stays
// in the storage contract for forward-compat if the API ever adds it.)
func TestTaxonomyRunnerSweepsAbsentRows(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := taxonomy.NewStore(pool)

	old := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	current := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	// Seed: a previously synced client + site that the upcoming run will not see.
	staleClientID, err := store.UpsertClient(ctx, taxonomy.ClientRow{
		ExternalID: "99", Name: "Client #99", SyncedAt: old,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "999", Name: "Old Site", ClientID: staleClientID,
		BrandName: "Burger King", BrandExternalID: "12", Active: true, SyncedAt: old,
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/signin":
			_, _ = w.Write([]byte(taxoSigninJSON))
		case "/brand":
			_, _ = w.Write([]byte(`[{"id":12,"name":"Burger King"}]`))
		case "/brand/12/store":
			// Only site 50 is in the current payload; site 999 (seeded above) is absent.
			_, _ = w.Write([]byte(`[{"id":50,"name":"Fresh","client_id":14,"brand_id":12}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	runner := taxonomy.NewRunner(
		taxonomy.NewClient(srv.URL, "u", "p"),
		store,
		func() time.Time { return current },
	)
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := map[string]bool{
		"50":  true,  // present in current walk
		"999": false, // absent from current walk → sweep
	}
	rows, err := pool.Query(ctx, `SELECT external_id, active FROM sites`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var ext string
		var active bool
		if err := rows.Scan(&ext, &active); err != nil {
			t.Fatal(err)
		}
		got[ext] = active
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("site %s: active=%v want %v (full got=%+v)", k, got[k], v, got)
		}
	}

	// Client 99 (no longer referenced by any current store) was also swept inactive.
	var oldActive bool
	if err := pool.QueryRow(ctx,
		`SELECT active FROM clients WHERE external_id = '99'`).Scan(&oldActive); err != nil {
		t.Fatal(err)
	}
	if oldActive {
		t.Errorf("client 99: active=true after sweep — want false")
	}
}

// TestTaxonomyRunnerAdvisoryLockSkipsConcurrent locks ADR-033 § 8: a
// second concurrent invocation exits gracefully without doing work
// when the per-process advisory lock (key 0x74786E73796E63) is
// already held. The first runner is parked mid-walk via a blocking
// /brand handler; the second runner must return nil without issuing
// any HTTP calls.
func TestTaxonomyRunnerAdvisoryLockSkipsConcurrent(t *testing.T) {
	requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	brandSeen := make(chan struct{}, 4)
	brandBlock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/signin":
			_, _ = w.Write([]byte(taxoSigninJSON))
		case "/brand":
			brandSeen <- struct{}{}
			<-brandBlock
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	now := func() time.Time { return time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC) }
	r1 := taxonomy.NewRunner(taxonomy.NewClient(srv.URL, "u", "p"), taxonomy.NewStore(pool), now)
	r2 := taxonomy.NewRunner(taxonomy.NewClient(srv.URL, "u", "p"), taxonomy.NewStore(pool), now)

	firstDone := make(chan error, 1)
	go func() { firstDone <- r1.Run(ctx) }()

	// Wait until r1 has signed in, acquired the lock, and is parked inside /brand.
	select {
	case <-brandSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("r1 never reached /brand")
	}

	// r2 must return promptly without making any HTTP calls and without erroring.
	r2Done := make(chan error, 1)
	go func() { r2Done <- r2.Run(ctx) }()
	select {
	case err := <-r2Done:
		if err != nil {
			t.Errorf("r2: got err %v want nil (graceful no-op when locked)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("r2 did not return within 2s — advisory lock did not gate; r2 is blocked on /brand")
	}

	// No second /brand hit while r1 was holding.
	select {
	case <-brandSeen:
		t.Errorf("r2 hit /brand — advisory lock did not gate")
	default:
	}

	close(brandBlock)
	if err := <-firstDone; err != nil {
		t.Errorf("r1: %v", err)
	}
}

// TestTaxonomyRunnerDryRunWalksWithoutWriting locks #18's --dry-run
// contract: the binary still signs in + walks /brand + /brand/{id}/store
// (so a misconfigured Cognito user or unparseable payload still
// surfaces an error) but writes nothing to Postgres. This is the
// "exercises auth + parsing without writing" path operators use
// from bench to validate creds before the real run.
func TestTaxonomyRunnerDryRunWalksWithoutWriting(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/signin":
			_, _ = w.Write([]byte(taxoSigninJSON))
		case "/brand":
			_, _ = w.Write([]byte(`[{"id":12,"name":"BK"}]`))
		case "/brand/12/store":
			_, _ = w.Write([]byte(`[{"id":50,"name":"Site","client_id":14,"brand_id":12}]`))
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
	if err := runner.RunDryRun(ctx); err != nil {
		t.Fatalf("RunDryRun: %v", err)
	}

	wantHits := []string{"/user/signin", "/brand", "/brand/12/store"}
	if len(hits) != len(wantHits) {
		t.Fatalf("hits: got %v want %v", hits, wantHits)
	}
	for i := range wantHits {
		if hits[i] != wantHits[i] {
			t.Errorf("hits[%d]: got %q want %q", i, hits[i], wantHits[i])
		}
	}

	// Postgres untouched.
	var clientCount, siteCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM clients`).Scan(&clientCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sites`).Scan(&siteCount); err != nil {
		t.Fatal(err)
	}
	if clientCount != 0 || siteCount != 0 {
		t.Errorf("dry-run wrote to Postgres: clients=%d sites=%d", clientCount, siteCount)
	}
}
