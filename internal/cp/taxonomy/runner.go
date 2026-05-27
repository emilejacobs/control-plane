package taxonomy

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

// Run executes one sync pass. The Postgres advisory lock (ADR-033 § 8)
// gates concurrency: a second invocation while one is in flight exits
// gracefully without doing any upstream HTTP work — the scheduled
// daily run and the manual button race-free.
func (r *Runner) Run(ctx context.Context) error {
	return r.run(ctx, false)
}

// RunDryRun signs in and walks /brand + /brand/{id}/store the same way
// Run does, but skips every Postgres write. Used by the binary's
// --dry-run flag to validate Cognito credentials and payload parsing
// from bench without touching the mirror tables.
func (r *Runner) RunDryRun(ctx context.Context) error {
	return r.run(ctx, true)
}

func (r *Runner) run(ctx context.Context, dryRun bool) error {
	if !dryRun {
		release, gotLock, err := r.store.AcquireSyncLock(ctx)
		if err != nil {
			return fmt.Errorf("acquire sync lock: %w", err)
		}
		if !gotLock {
			slog.InfoContext(ctx, "taxonomy.sync.skipped", "reason", "advisory_lock_held")
			return nil
		}
		defer release()
	}

	syncStart := r.now()
	if _, err := r.client.SignIn(ctx); err != nil {
		return fmt.Errorf("signin: %w", err)
	}
	brands, err := r.client.GetBrands(ctx)
	if err != nil {
		return fmt.Errorf("get brands: %w", err)
	}
	slog.InfoContext(ctx, "taxonomy.sync.start",
		"brands", len(brands), "started_at", syncStart, "dry_run", dryRun)

	for _, brand := range brands {
		stores, err := r.client.GetStores(ctx, brand.ID)
		if err != nil {
			return fmt.Errorf("get stores for brand %d: %w", brand.ID, err)
		}
		if dryRun {
			continue
		}
		brandExtID := strconv.Itoa(brand.ID)
		for _, store := range stores {
			clientExtID := strconv.Itoa(store.ClientID)
			clientLocalID, err := r.store.UpsertClient(ctx, ClientRow{
				ExternalID: clientExtID,
				// Upstream exposes no client metadata — only client_id.
				// Synthesize a placeholder until the API surfaces names;
				// dashboard renders "Client #14" in pickers. Tracked as
				// a #18 follow-up.
				Name:     "Client #" + clientExtID,
				SyncedAt: syncStart,
			})
			if err != nil {
				return fmt.Errorf("upsert client %d: %w", store.ClientID, err)
			}
			if _, err := r.store.UpsertSite(ctx, SiteRow{
				ExternalID:      strconv.Itoa(store.ID),
				Name:            store.Name,
				ClientID:        clientLocalID,
				BrandName:       brand.Name,
				BrandExternalID: brandExtID,
				// Upstream `/brand/{id}/store` has no per-row active flag;
				// a store returned by the walk IS active. Absence-from-walk
				// is the sole soft-delete signal (handled by SweepInactive).
				Active:   true,
				SyncedAt: syncStart,
			}); err != nil {
				return fmt.Errorf("upsert site %d: %w", store.ID, err)
			}
		}
	}

	if !dryRun {
		// Sweep happens after a successful walk only: failing partway through
		// (e.g. /brand/{id}/store returns 5xx for one brand) leaves the
		// already-mirrored rows alone rather than nuking them on stale data.
		if err := r.store.SweepInactive(ctx, syncStart); err != nil {
			return fmt.Errorf("sweep inactive: %w", err)
		}
	}

	slog.InfoContext(ctx, "taxonomy.sync.done", "started_at", syncStart, "dry_run", dryRun)
	return nil
}
