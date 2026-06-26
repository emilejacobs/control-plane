package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// FleetUnhealthy carries the offline-since (presence_changed_at) on offline
// signals so the notification reconciler can debounce sub-grace blips. The
// other kinds carry a nil Since (not debounced).
func TestFleetUnhealthyOfflineCarriesSince(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	dev := enrollForTest(t, srv, "mac-offline-since", "abababab-abab-abab-abab-abababababab")

	offlineAt := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Second)
	if _, err := srv.Pool.Exec(ctx,
		`UPDATE devices SET is_online = false, presence_changed_at = $2 WHERE id = $1`,
		dev, offlineAt); err != nil {
		t.Fatalf("mark offline: %v", err)
	}

	sigs, err := srv.Registry.FleetUnhealthy(ctx)
	if err != nil {
		t.Fatalf("FleetUnhealthy: %v", err)
	}

	var found *registry.UnhealthySignal
	for i := range sigs {
		if sigs[i].Kind == registry.UnhealthyOffline && sigs[i].DeviceID == dev {
			found = &sigs[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("offline signal for %s not returned by FleetUnhealthy", dev)
	}
	if found.Since == nil {
		t.Fatalf("offline signal Since is nil; want %v (presence_changed_at)", offlineAt)
	}
	if !found.Since.Equal(offlineAt) {
		t.Errorf("offline Since: got %v want %v", found.Since.UTC(), offlineAt)
	}
}
