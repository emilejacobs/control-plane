package integration_test

import (
	"context"
	"sort"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authz"
)

// insertClient inserts a clients row and returns its id.
func insertClient(t *testing.T, ctx context.Context, srv *testServer, name string) string {
	t.Helper()
	var id string
	if err := srv.Pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	return id
}

// insertSite inserts a sites row and returns its id.
func insertSite(t *testing.T, ctx context.Context, srv *testServer, clientID, name string) string {
	t.Helper()
	var id string
	if err := srv.Pool.QueryRow(ctx,
		`INSERT INTO sites (client_id, name) VALUES ($1, $2) RETURNING id`, clientID, name,
	).Scan(&id); err != nil {
		t.Fatalf("insert site: %v", err)
	}
	return id
}

// insertNonStaffOperator inserts a non-staff operator and returns its id.
func insertNonStaffOperator(t *testing.T, ctx context.Context, srv *testServer, email string) string {
	t.Helper()
	var id string
	if err := srv.Pool.QueryRow(ctx,
		`INSERT INTO operators (email, password_hash, is_staff) VALUES ($1, 'unused-hash', false) RETURNING id`,
		email,
	).Scan(&id); err != nil {
		t.Fatalf("insert non-staff operator: %v", err)
	}
	return id
}

// grantSite grants an operator access to a site.
func grantSite(t *testing.T, ctx context.Context, srv *testServer, operatorID, siteID string) {
	t.Helper()
	if _, err := srv.Pool.Exec(ctx,
		`INSERT INTO operator_sites (operator_id, site_id) VALUES ($1, $2)`, operatorID, siteID,
	); err != nil {
		t.Fatalf("grant site: %v", err)
	}
}

// insertDeviceAtSite inserts a devices row tied to a site and returns its id.
func insertDeviceAtSite(t *testing.T, ctx context.Context, srv *testServer, hostname, siteID string) string {
	t.Helper()
	var id string
	if err := srv.Pool.QueryRow(ctx, `
		INSERT INTO devices (hostname, hardware_uuid, hardware_kind, os_version,
		                     agent_version, iot_thing_arn, mtls_cert_arn, site_id)
		VALUES ($1, $2, 'mac', 'macOS 15.0', '0.1.0', 'arn:thing', 'arn:cert', $3)
		RETURNING id
	`, hostname, "hw-"+hostname, siteID).Scan(&id); err != nil {
		t.Fatalf("insert device at site: %v", err)
	}
	return id
}

// countDeviceQuery runs a (sql, args) pair from ScopedDeviceQuery and returns
// the number of device rows it yields.
func countDeviceQuery(t *testing.T, ctx context.Context, srv *testServer, sql string, args []any) int {
	t.Helper()
	rows, err := srv.Pool.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("run scoped query: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate scoped query: %v", err)
	}
	return n
}

func TestScopedDeviceQueryStaffSeesAllDevices(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "Acme Corp")
	siteA := insertSite(t, ctx, srv, clientID, "Acme HQ")
	siteB := insertSite(t, ctx, srv, clientID, "Acme Warehouse")
	insertDeviceAtSite(t, ctx, srv, "mac-a", siteA)
	insertDeviceAtSite(t, ctx, srv, "mac-b", siteB)

	// A staff filter imposes no restriction — every device is visible.
	sql, args := authz.ScopedDeviceQuery(authz.SiteFilter{All: true},
		"SELECT id FROM devices WHERE true")
	if n := countDeviceQuery(t, ctx, srv, sql, args); n != 2 {
		t.Errorf("staff scoped query: got %d devices want 2", n)
	}
}

func TestScopedDeviceQueryNonStaffSeesOnlyGrantedSites(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "Acme Corp")
	siteA := insertSite(t, ctx, srv, clientID, "Acme HQ")
	siteB := insertSite(t, ctx, srv, clientID, "Acme Warehouse")
	insertDeviceAtSite(t, ctx, srv, "mac-a1", siteA)
	insertDeviceAtSite(t, ctx, srv, "mac-a2", siteA)
	insertDeviceAtSite(t, ctx, srv, "mac-b1", siteB)

	// A non-staff operator granted only site A sees only site A's two devices.
	scopedSQL, scopedArgs := authz.ScopedDeviceQuery(
		authz.SiteFilter{SiteIDs: []string{siteA}}, "SELECT id FROM devices WHERE true")
	if n := countDeviceQuery(t, ctx, srv, scopedSQL, scopedArgs); n != 2 {
		t.Errorf("non-staff scoped query (site A): got %d devices want 2", n)
	}

	// Staff sees all three — the contrast AC3 asks for.
	staffSQL, staffArgs := authz.ScopedDeviceQuery(
		authz.SiteFilter{All: true}, "SELECT id FROM devices WHERE true")
	if n := countDeviceQuery(t, ctx, srv, staffSQL, staffArgs); n != 3 {
		t.Errorf("staff scoped query: got %d devices want 3", n)
	}
}

func TestScopeForNonStaffOperatorListsGrantedSites(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "Acme Corp")
	siteA := insertSite(t, ctx, srv, clientID, "Acme HQ")
	siteB := insertSite(t, ctx, srv, clientID, "Acme Warehouse")
	insertSite(t, ctx, srv, clientID, "Acme Annex") // not granted

	operatorID := insertNonStaffOperator(t, ctx, srv, "field-op@acme.test")
	grantSite(t, ctx, srv, operatorID, siteA)
	grantSite(t, ctx, srv, operatorID, siteB)

	f, err := authz.New(srv.Pool).ScopeForOperator(ctx, operatorID, false)
	if err != nil {
		t.Fatalf("ScopeForOperator: %v", err)
	}
	if f.All {
		t.Errorf("non-staff operator: All = true, want false")
	}
	got := append([]string(nil), f.SiteIDs...)
	want := []string{siteA, siteB}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) || (len(got) == 2 && (got[0] != want[0] || got[1] != want[1])) {
		t.Errorf("SiteIDs = %v, want the two granted sites %v", f.SiteIDs, want)
	}
}
