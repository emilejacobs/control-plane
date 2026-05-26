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
