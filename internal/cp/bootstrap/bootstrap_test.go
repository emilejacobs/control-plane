package bootstrap

import (
	"context"
	"errors"
	"testing"
)

// fakeLoader stands in for the Secrets Manager loader. It yields keys[i] on
// the i-th Load call (repeating the last), or err if set.
type fakeLoader struct {
	keys  []string
	err   error
	calls int
}

func (f *fakeLoader) Load(_ context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	i := f.calls
	if i >= len(f.keys) {
		i = len(f.keys) - 1
	}
	f.calls++
	return f.keys[i], nil
}

func TestVerifierChecksPresentedKey(t *testing.T) {
	v, err := NewVerifier(context.Background(), &fakeLoader{keys: []string{"secret-key"}})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if !v.Verify(context.Background(), "secret-key") {
		t.Errorf("Verify rejected the matching key")
	}
	if v.Verify(context.Background(), "wrong-key") {
		t.Errorf("Verify accepted a non-matching key")
	}
}

func TestVerifierReloadsAndAcceptsRotatedKey(t *testing.T) {
	// The key rotates: construction caches "old-key"; Secrets Manager now
	// holds "new-key" (e.g. a package rebuilt after a rotation).
	loader := &fakeLoader{keys: []string{"old-key", "new-key"}}
	v, err := NewVerifier(context.Background(), loader)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	if !v.Verify(context.Background(), "new-key") {
		t.Errorf("Verify rejected the rotated key instead of reloading")
	}
	if loader.calls != 2 {
		t.Errorf("loader called %d times, want 2 (one eager + one reload)", loader.calls)
	}
}

func TestNewVerifierFailsFastOnLoaderError(t *testing.T) {
	_, err := NewVerifier(context.Background(), &fakeLoader{err: errors.New("secrets manager unreachable")})
	if err == nil {
		t.Errorf("NewVerifier returned nil error when the loader failed")
	}
}
