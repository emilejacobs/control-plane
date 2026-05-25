package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

// Phase 2 followups #01: rows whose last_reported is older than the
// threshold get deleted (because an operator removed the service from
// the device's allow-list, so the agent stopped reporting on it).
// Fresh rows stay. The threshold is wall-clock; the sweeper itself
// doesn't run in this test — we just exercise the storage method.
func TestRegistryDeleteStaleDeviceServices(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-sweep-01", "44444444-4444-4444-4444-aaaaaaaaaaaa")

	// Two states: one observed 30 min ago (stale), one observed 1 min
	// ago (fresh). Threshold = 15 min should drop the stale one.
	now := time.Now().UTC()
	stale := now.Add(-30 * time.Minute)
	fresh := now.Add(-1 * time.Minute)

	if err := srv.Registry.RecordServiceStates(ctx, deviceID, []servicestatus.ServiceState{
		{Name: "removed.service", State: service.StateRunning, StateSince: stale},
	}, stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	if err := srv.Registry.RecordServiceStates(ctx, deviceID, []servicestatus.ServiceState{
		{Name: "active.service", State: service.StateRunning, StateSince: fresh},
	}, fresh); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	threshold := now.Add(-15 * time.Minute)
	n, err := srv.Registry.DeleteStaleDeviceServices(ctx, threshold)
	if err != nil {
		t.Fatalf("DeleteStaleDeviceServices: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted: got %d, want 1", n)
	}

	// Verify directly via SQL — the public ListServices reads through
	// the same scope-aware query so this is the cleanest check.
	rows, err := srv.Pool.Query(ctx, `SELECT service_name FROM device_services WHERE device_id = $1`, deviceID)
	if err != nil {
		t.Fatalf("query device_services: %v", err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if len(names) != 1 || names[0] != "active.service" {
		t.Errorf("remaining rows: got %v, want [active.service]", names)
	}
}

// Empty case: no rows match the threshold; returns 0, nil error.
func TestRegistryDeleteStaleDeviceServicesEmpty(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	n, err := srv.Registry.DeleteStaleDeviceServices(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteStaleDeviceServices: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted: got %d, want 0", n)
	}
	// Compile-time sanity that errors.Is still works (unused-import guard)
	_ = errors.Is
}
