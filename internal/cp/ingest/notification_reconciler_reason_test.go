package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// Issue #158: when a device-offline alert recovers, the reconciler tags the
// resolved event with the offline reason from the store, so the recovery digest
// can render "reboot: <cause>" vs "network blip". Non-offline kinds and unknown
// (old-agent) devices carry no reason.
func TestReconcilerTagsOfflineRecoveryReason(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

	store := newFakeAlertStore()
	notifier := &fakeNotifier{}
	rec := newReconciler(store, notifier, now)

	// Two devices go offline and get an open+notified alert (tick 1).
	store.setSnapshot(offline("dev-reboot", "mac-r"), offline("dev-blip", "mac-b"))
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick 1: %v", err)
	}

	// Both recover; the store classifies one as a reboot, the other a blip.
	store.setReason("dev-reboot", "reboot: clean restart")
	store.setReason("dev-blip", "network blip")
	store.setSnapshot() // empty snapshot = both cleared
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	digests := notifier.calls()
	if len(digests) != 2 {
		t.Fatalf("notifier digests: got %d want 2 (open + recover)", len(digests))
	}
	resolved := digests[1].Resolved
	if len(resolved) != 2 {
		t.Fatalf("resolved events: got %d want 2", len(resolved))
	}
	byDevice := map[string]string{}
	for _, e := range resolved {
		byDevice[e.DeviceID] = e.Reason
	}
	if got := byDevice["dev-reboot"]; got != "reboot: clean restart" {
		t.Errorf("dev-reboot reason: got %q want reboot: clean restart", got)
	}
	if got := byDevice["dev-blip"]; got != "network blip" {
		t.Errorf("dev-blip reason: got %q want network blip", got)
	}
}

// A recovered service_stopped alert (not offline) never gets a reason — the
// reason is offline-specific, and OfflineReason isn't even consulted.
func TestReconcilerNoReasonForNonOffline(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

	store := newFakeAlertStore()
	notifier := &fakeNotifier{}
	rec := newReconciler(store, notifier, now)

	stopped := registry.UnhealthySignal{Kind: registry.UnhealthyServiceStopped, DeviceID: "dev-x", Hostname: "mac-x", Subject: "alpr"}
	store.setSnapshot(stopped)
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	// If OfflineReason were (wrongly) consulted, this would leak onto the event.
	store.setReason("dev-x", "reboot: clean restart")
	store.setSnapshot()
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	resolved := notifier.calls()[1].Resolved
	if len(resolved) != 1 || resolved[0].Reason != "" {
		t.Errorf("service_stopped recovery should carry no reason; got %+v", resolved)
	}
}
