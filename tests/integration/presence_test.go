package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// TestRegistryUpdateLastSeen is Issue 07 cycle 2: Registry.UpdateLastSeen
// stamps devices.last_seen, GetByID surfaces it, and an id matching no row
// (or not a UUID at all) is reported as ErrDeviceNotFound.
func TestRegistryUpdateLastSeen(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	deviceID := enrollForTest(t, srv, "mac-mini-presence-01", "33333333-3333-3333-4444-555555555555")

	// A freshly enrolled device has never been seen.
	dev, err := srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if dev.LastSeen != nil {
		t.Errorf("fresh device last_seen: got %v want nil", dev.LastSeen)
	}

	// UpdateLastSeen stamps it, and GetByID reads it back.
	at := time.Date(2026, 5, 21, 12, 30, 0, 0, time.UTC)
	if err := srv.Registry.UpdateLastSeen(ctx, deviceID, at); err != nil {
		t.Fatalf("UpdateLastSeen: %v", err)
	}
	dev, err = srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if dev.LastSeen == nil {
		t.Fatalf("last_seen still nil after UpdateLastSeen")
	}
	if !dev.LastSeen.Equal(at) {
		t.Errorf("last_seen: got %v want %v", dev.LastSeen, at)
	}

	// An unknown but well-formed device id.
	unknownID := "00000000-0000-0000-0000-000000000000"
	if err := srv.Registry.UpdateLastSeen(ctx, unknownID, at); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("unknown device id: got %v want ErrDeviceNotFound", err)
	}

	// A device id that isn't a UUID — must not surface as a DB error.
	if err := srv.Registry.UpdateLastSeen(ctx, "not-a-uuid", at); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("non-uuid device id: got %v want ErrDeviceNotFound", err)
	}
}
