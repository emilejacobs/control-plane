package taxonomy

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
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

// collectedStore carries a single store plus the brand it was walked
// under, so the second-pass site upsert can stamp brand metadata
// without re-iterating brands.
type collectedStore struct {
	store       UpstreamStore
	brandID     string
	brandName   string
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

	// First pass: walk every brand's stores, collect them in memory,
	// and record which brands each client operates. The two-pass shape
	// lets the second pass upsert each client exactly once with the
	// joined list of all brands they operate — the brand-as-client-name
	// substitute until the upstream API exposes real client metadata.
	// Memory is bounded: <100 clients × <10 brands × ~16 bytes/name +
	// <500 stores × ~200 bytes/row.
	clientBrands := map[int]map[string]struct{}{}
	var collected []collectedStore
	for _, brand := range brands {
		stores, err := r.client.GetStores(ctx, brand.ID)
		if err != nil {
			return fmt.Errorf("get stores for brand %d: %w", brand.ID, err)
		}
		if dryRun {
			continue
		}
		brandExtID := strconv.Itoa(brand.ID)
		for _, s := range stores {
			if _, ok := clientBrands[s.ClientID]; !ok {
				clientBrands[s.ClientID] = map[string]struct{}{}
			}
			clientBrands[s.ClientID][brand.Name] = struct{}{}
			collected = append(collected, collectedStore{
				store:     s,
				brandID:   brandExtID,
				brandName: brand.Name,
			})
		}
	}

	if dryRun {
		slog.InfoContext(ctx, "taxonomy.sync.done", "started_at", syncStart, "dry_run", true)
		return nil
	}

	// Second pass: upsert clients keyed by upstream client_id, with
	// the joined brand names as clients.name. Build the
	// external_id → local UUID map sites need as their FK target.
	clientLocalIDs := make(map[int]string, len(clientBrands))
	for extID, brandSet := range clientBrands {
		clientLocalID, err := r.store.UpsertClient(ctx, ClientRow{
			ExternalID: strconv.Itoa(extID),
			Name:       joinBrandNames(brandSet),
			SyncedAt:   syncStart,
		})
		if err != nil {
			return fmt.Errorf("upsert client %d: %w", extID, err)
		}
		clientLocalIDs[extID] = clientLocalID
	}

	// Third pass: upsert sites with the now-known local client UUIDs.
	for _, c := range collected {
		if _, err := r.store.UpsertSite(ctx, SiteRow{
			ExternalID:      strconv.Itoa(c.store.ID),
			Name:            c.store.Name,
			ClientID:        clientLocalIDs[c.store.ClientID],
			BrandName:       c.brandName,
			BrandExternalID: c.brandID,
			// Upstream `/brand/{id}/store` has no per-row active flag;
			// a store returned by the walk IS active. Absence-from-walk
			// is the sole soft-delete signal (handled by SweepInactive).
			Active:   true,
			SyncedAt: syncStart,
		}); err != nil {
			return fmt.Errorf("upsert site %d: %w", c.store.ID, err)
		}
	}

	// Sweep happens after a successful walk only: failing partway through
	// (e.g. /brand/{id}/store returns 5xx for one brand) leaves the
	// already-mirrored rows alone rather than nuking them on stale data.
	if err := r.store.SweepInactive(ctx, syncStart); err != nil {
		return fmt.Errorf("sweep inactive: %w", err)
	}

	slog.InfoContext(ctx, "taxonomy.sync.done", "started_at", syncStart, "dry_run", false)
	return nil
}

// joinBrandNames renders the set of brands a client operates as a
// sorted, comma-separated label — the brand-name substitute for
// real client identity (#18 follow-up). Single-brand clients get
// their single brand back; multi-brand clients honestly disclose.
// Empty input returns "" (would only happen on a sync where a client
// appears with no associated brand walks — should be impossible by
// construction since the set is populated during the brand walks).
func joinBrandNames(brands map[string]struct{}) string {
	names := make([]string, 0, len(brands))
	for n := range brands {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
