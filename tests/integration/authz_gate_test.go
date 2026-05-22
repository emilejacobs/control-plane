package integration_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authz"
)

// TestScopedDeviceQueryGate is the CI gate of PRD user story 25: every
// device-returning handler must route through ScopedDeviceQuery. A pgx tracer
// records the SQL each request runs; authz.UnscopedDeviceReads flags any
// devices read that lacks the scoped marker. The real handlers must pass; a
// query that bypasses the helper must be caught.
func TestScopedDeviceQueryGate(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv, rec := newTracedTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "Acme Corp")
	siteA := insertSite(t, ctx, srv, clientID, "Acme HQ")
	deviceID := insertDeviceAtSite(t, ctx, srv, "mac-a", siteA)
	token := mintAccessToken(t, ctx, srv)

	// The real device-read handlers — GET /devices and GET /devices/{id} —
	// must route every devices query through ScopedDeviceQuery.
	rec.reset()
	doDeviceList(t, srv.URL, token)
	get := doDeviceGet(t, srv.URL, deviceID, token)
	get.Body.Close()
	if bad := authz.UnscopedDeviceReads(rec.snapshot()); len(bad) != 0 {
		t.Errorf("real device handlers bypassed ScopedDeviceQuery:\n%v", bad)
	}

	// A handler that read the devices table directly would bypass the helper.
	// The gate must catch such a query.
	rec.reset()
	rows, err := srv.Pool.Query(ctx, "SELECT id FROM devices")
	if err != nil {
		t.Fatalf("raw devices query: %v", err)
	}
	rows.Close()
	if bad := authz.UnscopedDeviceReads(rec.snapshot()); len(bad) == 0 {
		t.Error("gate did not catch a raw, unscoped devices read")
	}
}
