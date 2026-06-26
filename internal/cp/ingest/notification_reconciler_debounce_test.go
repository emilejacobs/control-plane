package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

func offlineSince(deviceID, hostname string, since time.Time) registry.UnhealthySignal {
	s := offline(deviceID, hostname)
	s.Since = &since
	return s
}

// Offline debounce (network-blip noise): a device that's been offline for less
// than the grace window does NOT open or notify — so a sub-grace blip never
// fires an OFFLINE + recovered pair. The same device, once offline past the
// grace, fires normally.
func TestReconcilerDebouncesShortOffline(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	store := newFakeAlertStore()
	notifier := &fakeNotifier{}
	cs := &fakeConfigSource{}
	cs.set(ingest.NotificationConfig{Enabled: true, OfflineGrace: 3 * time.Minute})
	rec := newReconcilerWithConfig(store, notifier, cs, now)

	// Offline for only 1 minute → within grace → suppressed entirely.
	store.setSnapshot(offlineSince("dev-blip", "mac-b", now.Add(-1*time.Minute)))
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick (within grace): %v", err)
	}
	if len(notifier.calls()) != 0 {
		t.Errorf("a sub-grace offline should not notify; got %d digests", len(notifier.calls()))
	}
	if _, ok := store.get(alertKey{registry.UnhealthyOffline, "dev-blip", ""}); ok {
		t.Errorf("a sub-grace offline should not open an alert row")
	}
}

// A device offline longer than the grace window fires normally.
func TestReconcilerFiresSustainedOffline(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	store := newFakeAlertStore()
	notifier := &fakeNotifier{}
	cs := &fakeConfigSource{}
	cs.set(ingest.NotificationConfig{Enabled: true, OfflineGrace: 3 * time.Minute})
	rec := newReconcilerWithConfig(store, notifier, cs, now)

	store.setSnapshot(offlineSince("dev-down", "mac-d", now.Add(-5*time.Minute)))
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick (past grace): %v", err)
	}
	digests := notifier.calls()
	if len(digests) != 1 || len(digests[0].Opened) != 1 || digests[0].Opened[0].DeviceID != "dev-down" {
		t.Fatalf("a sustained offline should fire one opened alert; got %+v", digests)
	}
}

// The debounce is offline-only: a service_stopped signal fires immediately,
// regardless of the grace window (it has no Since).
func TestReconcilerDebounceIsOfflineOnly(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	store := newFakeAlertStore()
	notifier := &fakeNotifier{}
	cs := &fakeConfigSource{}
	cs.set(ingest.NotificationConfig{Enabled: true, OfflineGrace: 3 * time.Minute})
	rec := newReconcilerWithConfig(store, notifier, cs, now)

	store.setSnapshot(registry.UnhealthySignal{
		Kind: registry.UnhealthyServiceStopped, DeviceID: "dev-svc", Hostname: "mac-s", Subject: "alpr",
	})
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	digests := notifier.calls()
	if len(digests) != 1 || len(digests[0].Opened) != 1 {
		t.Fatalf("service_stopped must fire immediately (not debounced); got %+v", digests)
	}
}

// Grace 0 disables the debounce — an offline alert fires immediately (the
// pre-debounce behaviour), even with a fresh Since.
func TestReconcilerZeroGraceDisablesDebounce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	store := newFakeAlertStore()
	notifier := &fakeNotifier{}
	cs := &fakeConfigSource{}
	cs.set(ingest.NotificationConfig{Enabled: true, OfflineGrace: 0})
	rec := newReconcilerWithConfig(store, notifier, cs, now)

	store.setSnapshot(offlineSince("dev-x", "mac-x", now.Add(-10*time.Second)))
	if err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(notifier.calls()) != 1 {
		t.Errorf("grace 0 should fire immediately; got %d digests", len(notifier.calls()))
	}
}
