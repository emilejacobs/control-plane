package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

// TestIdempotencyGate is the structural-rule enforcement test promised by
// ADR-012 and Issue 03 acceptance criteria. It builds the production router,
// enumerates every state-mutating route the builder registered, and asserts
// each one rejects requests that omit Idempotency-Key with HTTP 400. A new
// POST/PUT/PATCH/DELETE that bypasses the builder (and therefore the
// middleware) will not appear in the route table; routes that *do* go through
// the builder are guaranteed to be wrapped. This test fails if the wrapping
// stops doing what we say it does.
func TestIdempotencyGate(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	iot := iotprovisioner.NewFake()
	reg := registry.New(pool, iot, registry.Config{BootstrapKey: testBootstrapKey})
	store := storage.NewIdempotencyStore(pool)

	builder := api.NewBuilderWith(api.Deps{
		Registry:             reg,
		IdempotencyStore:     store,
		DevDevicesGetEnabled: true,
	})
	srv := httptest.NewServer(builder.Handler())
	t.Cleanup(srv.Close)

	routes := builder.MutatingRoutes()
	if len(routes) == 0 {
		t.Fatalf("no mutating routes registered; gate has nothing to verify")
	}

	for _, route := range routes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			req, err := http.NewRequest(route.Method, srv.URL+route.Path, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			// Deliberately no Idempotency-Key.

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s %s without Idempotency-Key: got %d want 400 (idempotency middleware appears unwired for this route)",
					route.Method, route.Path, resp.StatusCode)
			}
		})
	}
}
