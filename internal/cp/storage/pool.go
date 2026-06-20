package storage

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultPasswordTTL bounds how long a fetched DB password is cached, and thus
// the self-heal window after an RDS master-password rotation: new connections
// keep failing with the stale password only until the entry expires and the
// live source is re-read (issue: db-dsn rotation outage).
const defaultPasswordTTL = 30 * time.Second

// PasswordFetcher returns the current database password from the source of
// truth (the RDS-managed secret). NewPool uses it to stamp the live password
// onto each new connection so a rotated password is picked up WITHOUT a task
// restart — the secrets-inject-at-task-start gap that left the control plane
// down until a forced redeploy.
type PasswordFetcher interface {
	FetchPassword(ctx context.Context) (string, error)
}

// PoolOptions configures NewPool.
type PoolOptions struct {
	// Fetcher, when non-nil, enables live password refresh via BeforeConnect.
	// Nil → the pool is identical to pgxpool.New(ctx, dsn).
	Fetcher PasswordFetcher
	// TTL bounds the cached-password staleness (zero → defaultPasswordTTL).
	TTL time.Duration
	// now is injected in tests; nil → time.Now.
	now func() time.Time
}

// NewPool builds a pgx pool from dsn. With opts.Fetcher set, new connections
// take their password from the live source (BeforeConnect); the dsn's own
// password is kept as the bootstrap/fallback used when a live fetch errors. With
// no Fetcher it behaves exactly like pgxpool.New(ctx, dsn).
func NewPool(ctx context.Context, dsn string, opts PoolOptions) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if opts.Fetcher != nil {
		cfg.BeforeConnect = newLivePassword(opts).beforeConnect
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// livePassword caches the DB password fetched from a PasswordFetcher, refreshing
// it once the TTL elapses. The mutex serialises the refresh so a burst of new
// connections doesn't stampede the live source.
type livePassword struct {
	fetch PasswordFetcher
	ttl   time.Duration
	now   func() time.Time

	mu  sync.Mutex
	val string
	exp time.Time
}

func newLivePassword(opts PoolOptions) *livePassword {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = defaultPasswordTTL
	}
	clk := opts.now
	if clk == nil {
		clk = time.Now
	}
	return &livePassword{fetch: opts.Fetcher, ttl: ttl, now: clk}
}

// beforeConnect stamps the current password onto the connection. With no value
// available (a fetch error and no cache yet) it leaves connCfg.Password as the
// dsn-seeded value, so a Secrets Manager hiccup is never worse than the static
// path.
func (l *livePassword) beforeConnect(ctx context.Context, connCfg *pgx.ConnConfig) error {
	if pw := l.current(ctx); pw != "" {
		connCfg.Password = pw
	}
	return nil
}

// current returns the cached password, refreshing it from the live source when
// the cache is empty or expired. A fetch error keeps the last known-good value
// (or "" if none yet) and never propagates — connecting must not block on
// Secrets Manager availability.
func (l *livePassword) current(ctx context.Context) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.val != "" && l.now().Before(l.exp) {
		return l.val
	}
	pw, err := l.fetch.FetchPassword(ctx)
	if err != nil || pw == "" {
		return l.val // last known-good, or "" → caller keeps the dsn password
	}
	l.val = pw
	l.exp = l.now().Add(l.ttl)
	return l.val
}
