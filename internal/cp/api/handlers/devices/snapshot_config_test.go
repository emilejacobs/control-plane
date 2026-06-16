package devices_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type snapshotConfigStore struct {
	mu      sync.Mutex
	known   map[string]bool
	cadence map[string]string
}

func (s *snapshotConfigStore) GetByID(_ context.Context, id string) (registry.Device, error) {
	if s.known[id] {
		return registry.Device{ID: id}, nil
	}
	return registry.Device{}, registry.ErrDeviceNotFound
}

func (s *snapshotConfigStore) SetSnapshotCadence(_ context.Context, deviceID, cadence string) error {
	s.mu.Lock()
	if s.cadence == nil {
		s.cadence = map[string]string{}
	}
	s.cadence[deviceID] = cadence
	s.mu.Unlock()
	return nil
}

func TestSnapshotConfigPutHappyPath(t *testing.T) {
	store := &snapshotConfigStore{known: map[string]bool{"dev-abc": true}}
	h := devices.NewSnapshotConfig(store)

	req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/snapshot-config",
		strings.NewReader(`{"cadence":"daily"}`))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	if store.cadence["dev-abc"] != "daily" {
		t.Errorf("persisted cadence = %q, want daily", store.cadence["dev-abc"])
	}
}

func TestSnapshotConfigPutRejectsBadCadence(t *testing.T) {
	store := &snapshotConfigStore{known: map[string]bool{"dev-abc": true}}
	h := devices.NewSnapshotConfig(store)

	req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/snapshot-config",
		strings.NewReader(`{"cadence":"hourly"}`))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if _, set := store.cadence["dev-abc"]; set {
		t.Error("an invalid cadence should not be persisted")
	}
}

func TestSnapshotConfigPutUnknownDevice(t *testing.T) {
	h := devices.NewSnapshotConfig(&snapshotConfigStore{known: map[string]bool{}})
	req := httptest.NewRequest(http.MethodPut, "/devices/missing/snapshot-config",
		strings.NewReader(`{"cadence":"weekly"}`))
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
