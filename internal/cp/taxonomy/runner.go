package taxonomy

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Runner orchestrates one sync run end-to-end: SignIn → walk active
// brands → walk each brand's stores → upsert clients and sites →
// sweep absent rows. The pieces are individually unit-testable; this
// shell is the integration boundary.
type Runner struct {
	client *Client
	store  *Store
	now    func() time.Time
}

// NewRunner binds an upstream API client and a Postgres-backed Store.
// `now` returns the sync run's start time (captured before any network
// call) so SweepInactive's cutoff is set deterministically; production
// passes time.Now, tests pass a fixed clock.
func NewRunner(client *Client, store *Store, now func() time.Time) *Runner {
	return &Runner{client: client, store: store, now: now}
}

// Run executes one sync pass. It is the single entry point cmd/taxonomy-sync
// calls; concurrency control (advisory lock) is layered around it.
func (r *Runner) Run(ctx context.Context) error {
	syncStart := r.now()
	if _, err := r.client.SignIn(ctx); err != nil {
		return fmt.Errorf("signin: %w", err)
	}
	brands, err := r.client.GetBrands(ctx)
	if err != nil {
		return fmt.Errorf("get brands: %w", err)
	}
	slog.InfoContext(ctx, "taxonomy.sync.start", "brands", len(brands), "started_at", syncStart)

	for _, brand := range brands {
		if !brand.Active {
			continue
		}
		stores, err := r.client.GetStores(ctx, brand.ID)
		if err != nil {
			return fmt.Errorf("get stores for brand %q: %w", brand.ID, err)
		}
		for _, store := range stores {
			clientLocalID, err := r.store.UpsertClient(ctx, ClientRow{
				ExternalID: store.Client.ID,
				Name:       store.Client.Name,
				SyncedAt:   syncStart,
			})
			if err != nil {
				return fmt.Errorf("upsert client %q: %w", store.Client.ID, err)
			}
			if _, err := r.store.UpsertSite(ctx, SiteRow{
				ExternalID:      store.ID,
				Name:            store.Name,
				ClientID:        clientLocalID,
				BrandName:       brand.Name,
				BrandExternalID: brand.ID,
				SyncedAt:        syncStart,
			}); err != nil {
				return fmt.Errorf("upsert site %q: %w", store.ID, err)
			}
		}
	}

	slog.InfoContext(ctx, "taxonomy.sync.done", "started_at", syncStart)
	return nil
}
