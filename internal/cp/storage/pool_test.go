package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type fetcherFunc func(context.Context) (string, error)

func (f fetcherFunc) FetchPassword(ctx context.Context) (string, error) { return f(ctx) }

// Within the TTL the live source is read once; repeat connects reuse the cache.
func TestLivePasswordCachesWithinTTL(t *testing.T) {
	calls := 0
	now := time.Unix(1000, 0)
	lp := newLivePassword(PoolOptions{
		Fetcher: fetcherFunc(func(context.Context) (string, error) { calls++; return "pw1", nil }),
		TTL:     30 * time.Second,
		now:     func() time.Time { return now },
	})
	if got := lp.current(context.Background()); got != "pw1" {
		t.Fatalf("first current = %q, want pw1", got)
	}
	if got := lp.current(context.Background()); got != "pw1" {
		t.Fatalf("second current = %q, want pw1", got)
	}
	if calls != 1 {
		t.Errorf("fetched %d times within TTL, want 1", calls)
	}
}

// After the TTL elapses the rotated password is picked up — the self-heal.
func TestLivePasswordRefetchesAfterTTL(t *testing.T) {
	seq := []string{"old", "new"}
	calls := 0
	now := time.Unix(1000, 0)
	lp := newLivePassword(PoolOptions{
		Fetcher: fetcherFunc(func(context.Context) (string, error) {
			v := seq[min(calls, len(seq)-1)]
			calls++
			return v, nil
		}),
		TTL: 30 * time.Second,
		now: func() time.Time { return now },
	})
	if got := lp.current(context.Background()); got != "old" {
		t.Fatalf("pre-rotation = %q, want old", got)
	}
	now = now.Add(31 * time.Second) // past TTL → rotation visible
	if got := lp.current(context.Background()); got != "new" {
		t.Errorf("post-TTL = %q, want new (rotated password)", got)
	}
}

// A fetch error after the cache expires keeps the last known-good value rather
// than going empty — a Secrets Manager hiccup must never block connecting.
func TestLivePasswordKeepsLastGoodOnError(t *testing.T) {
	calls := 0
	now := time.Unix(1000, 0)
	lp := newLivePassword(PoolOptions{
		Fetcher: fetcherFunc(func(context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "pw1", nil
			}
			return "", errors.New("sm unavailable")
		}),
		TTL: 1 * time.Second,
		now: func() time.Time { return now },
	})
	_ = lp.current(context.Background()) // caches pw1
	now = now.Add(2 * time.Second)       // expire
	if got := lp.current(context.Background()); got != "pw1" {
		t.Errorf("on fetch error = %q, want last-known-good pw1", got)
	}
}

// beforeConnect stamps the live password onto the connection.
func TestBeforeConnectStampsLivePassword(t *testing.T) {
	lp := newLivePassword(PoolOptions{
		Fetcher: fetcherFunc(func(context.Context) (string, error) { return "live", nil }),
	})
	cc, err := pgx.ParseConfig("postgresql://u:seed@h:5432/db")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if err := lp.beforeConnect(context.Background(), cc); err != nil {
		t.Fatalf("beforeConnect: %v", err)
	}
	if cc.Password != "live" {
		t.Errorf("Password = %q, want live", cc.Password)
	}
}

// With no value available (first fetch errors, no cache), beforeConnect leaves
// the dsn-seeded password in place — never worse than the static path.
func TestBeforeConnectPreservesDSNPasswordWhenEmpty(t *testing.T) {
	lp := newLivePassword(PoolOptions{
		Fetcher: fetcherFunc(func(context.Context) (string, error) { return "", errors.New("nope") }),
	})
	cc, err := pgx.ParseConfig("postgresql://u:seed@h:5432/db")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if err := lp.beforeConnect(context.Background(), cc); err != nil {
		t.Fatalf("beforeConnect: %v", err)
	}
	if cc.Password != "seed" {
		t.Errorf("Password = %q, want the dsn-seeded 'seed' preserved", cc.Password)
	}
}
