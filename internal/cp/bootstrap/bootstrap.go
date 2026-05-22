// Package bootstrap validates the static enrollment bootstrap key (ADR-017).
//
// The key's store of record is AWS Secrets Manager. The Verifier caches the
// key in memory and re-fetches it on a mismatch, so a key rotated mid-deploy
// is picked up without a service restart.
package bootstrap

import (
	"context"
	"crypto/subtle"
	"fmt"
	"sync"
)

// KeyLoader fetches the current bootstrap key from its store of record —
// AWS Secrets Manager in production, a fake in tests.
type KeyLoader interface {
	Load(ctx context.Context) (string, error)
}

// FixedKey is a KeyLoader that always yields the same key. It backs tests
// and never rotates.
type FixedKey string

func (k FixedKey) Load(context.Context) (string, error) { return string(k), nil }

// Verifier checks a presented bootstrap key against the key loaded from the
// store.
type Verifier struct {
	loader KeyLoader
	mu     sync.RWMutex
	key    string
}

// NewVerifier loads the bootstrap key eagerly; a loader error fails fast so
// a misconfigured service does not start serving enrollments.
func NewVerifier(ctx context.Context, loader KeyLoader) (*Verifier, error) {
	key, err := loader.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load bootstrap key: %w", err)
	}
	return &Verifier{loader: loader, key: key}, nil
}

// Verify reports whether presented is the current bootstrap key. On a
// mismatch it reloads the key once before rejecting — so a key rotated since
// the cached copy was fetched is honored without a service restart.
func (v *Verifier) Verify(ctx context.Context, presented string) bool {
	v.mu.RLock()
	cached := v.key
	v.mu.RUnlock()
	if matches(presented, cached) {
		return true
	}

	fresh, err := v.loader.Load(ctx)
	if err != nil {
		return false
	}
	v.mu.Lock()
	v.key = fresh
	v.mu.Unlock()
	return matches(presented, fresh)
}

// matches is a constant-time comparison — bootstrap keys are secrets.
func matches(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
