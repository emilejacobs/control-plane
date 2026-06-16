package enroll_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/enroll"
)

// Re-running Enroll on the same device is safe: it succeeds again with the
// same device id, and every attempt carries the hardware-UUID idempotency key
// (the CP dedups on it, so no duplicate device is registered — ADR-036).
func TestEnrollIsRepeatable(t *testing.T) {
	cp := newFakeCP(t)
	dir := t.TempDir()
	p := sampleParams(cp.srv.URL, dir)

	first, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatalf("Enroll #1: %v", err)
	}
	second, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatalf("Enroll #2: %v", err)
	}

	if first.DeviceID != second.DeviceID {
		t.Errorf("device id changed across runs: %q vs %q", first.DeviceID, second.DeviceID)
	}
	if cp.calls != 2 {
		t.Errorf("CP calls: got %d want 2", cp.calls)
	}
	if cp.gotIdempotencyKey != "HW-UUID-1" {
		t.Errorf("idempotency key on re-run: got %q", cp.gotIdempotencyKey)
	}
	// The agent-config is still present and well-formed after the re-run.
	if _, err := os.Stat(filepath.Join(dir, "agent-config.json")); err != nil {
		t.Errorf("agent-config missing after re-run: %v", err)
	}
}

// A rejected bootstrap key (HTTP 401) returns the sentinel error and leaves no
// half-written cert/key/config behind.
func TestEnrollInvalidBootstrapKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid bootstrap key", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	_, err := enroll.Enroll(context.Background(), sampleParams(srv.URL, dir))
	if !errors.Is(err, enroll.ErrInvalidBootstrapKey) {
		t.Fatalf("error: got %v want ErrInvalidBootstrapKey", err)
	}
	assertNoFiles(t, dir)
}

// Any other non-201 surfaces as a generic error, again with nothing written.
func TestEnrollServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	_, err := enroll.Enroll(context.Background(), sampleParams(srv.URL, dir))
	if err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
	if errors.Is(err, enroll.ErrInvalidBootstrapKey) {
		t.Errorf("500 should not map to ErrInvalidBootstrapKey")
	}
	assertNoFiles(t, dir)
}

func assertNoFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"cert.pem", "key.pem", "ca.pem", "agent-config.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be absent after a failed enroll (err=%v)", name, err)
		}
	}
}
