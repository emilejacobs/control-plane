package ingest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// alertKey is the (kind, device, subject) identity the fake store dedupes on.
type alertKey struct {
	kind     registry.UnhealthyKind
	deviceID string
	subject  string
}

// fakeAlertStore is a stateful in-memory stand-in for the registry's
// notification surface: FleetUnhealthy serves a settable snapshot, and the
// alert_state CRUD mutates an in-memory open set so multi-tick tests evolve
// state the way the real DB would.
type fakeAlertStore struct {
	mu       sync.Mutex
	snapshot []registry.UnhealthySignal
	open     map[alertKey]registry.OpenAlert
}

func newFakeAlertStore() *fakeAlertStore {
	return &fakeAlertStore{open: map[alertKey]registry.OpenAlert{}}
}

func (s *fakeAlertStore) setSnapshot(sigs ...registry.UnhealthySignal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = sigs
}

func (s *fakeAlertStore) FleetUnhealthy(context.Context) ([]registry.UnhealthySignal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]registry.UnhealthySignal(nil), s.snapshot...), nil
}

func (s *fakeAlertStore) LoadOpenAlerts(context.Context) ([]registry.OpenAlert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]registry.OpenAlert, 0, len(s.open))
	for _, a := range s.open {
		out = append(out, a)
	}
	return out, nil
}

func (s *fakeAlertStore) OpenAlert(_ context.Context, kind registry.UnhealthyKind, deviceID, subject string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := alertKey{kind, deviceID, subject}
	if _, ok := s.open[k]; !ok {
		s.open[k] = registry.OpenAlert{Kind: kind, DeviceID: deviceID, Subject: subject, OpenedAt: at}
	}
	return nil
}

func (s *fakeAlertStore) MarkAlertNotified(_ context.Context, kind registry.UnhealthyKind, deviceID, subject string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := alertKey{kind, deviceID, subject}
	if a, ok := s.open[k]; ok {
		t := at
		a.LastNotifiedAt = &t
		a.NotifyAttempts++
		s.open[k] = a
	}
	return nil
}

func (s *fakeAlertStore) ResolveAlert(_ context.Context, kind registry.UnhealthyKind, deviceID, subject string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.open, alertKey{kind, deviceID, subject})
	return nil
}

// openCount / notifiedAt are test helpers for asserting persisted state.
func (s *fakeAlertStore) get(k alertKey) (registry.OpenAlert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.open[k]
	return a, ok
}

// fakeNotifier records every digest it is asked to deliver, and can be told to
// fail to exercise the at-least-once retry path.
type fakeNotifier struct {
	mu       sync.Mutex
	digests  []ingest.Digest
	configs  []ingest.NotifyConfig
	failWith error
}

func (n *fakeNotifier) Notify(_ context.Context, d ingest.Digest, cfg ingest.NotifyConfig) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.digests = append(n.digests, d)
	n.configs = append(n.configs, cfg)
	return n.failWith
}

func (n *fakeNotifier) calls() []ingest.Digest {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]ingest.Digest(nil), n.digests...)
}

func (n *fakeNotifier) lastConfig() ingest.NotifyConfig {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.configs) == 0 {
		return ingest.NotifyConfig{}
	}
	return n.configs[len(n.configs)-1]
}

// fakeConfigSource serves a settable NotificationConfig.
type fakeConfigSource struct {
	mu  sync.Mutex
	cfg ingest.NotificationConfig
}

func (s *fakeConfigSource) Load(context.Context) (ingest.NotificationConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, nil
}

func (s *fakeConfigSource) set(cfg ingest.NotificationConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

func newReconciler(s ingest.NotificationStore, n ingest.Notifier, now time.Time) *ingest.NotificationReconciler {
	return ingest.NewNotificationReconciler(s, n, ingest.NotificationReconcilerConfig{
		Now: func() time.Time { return now },
	})
}

func newReconcilerWithConfig(s ingest.NotificationStore, n ingest.Notifier, cs ingest.ConfigSource, now time.Time) *ingest.NotificationReconciler {
	return ingest.NewNotificationReconciler(s, n, ingest.NotificationReconcilerConfig{
		ConfigSource: cs,
		Now:          func() time.Time { return now },
	})
}

func offline(deviceID, hostname string) registry.UnhealthySignal {
	return registry.UnhealthySignal{Kind: registry.UnhealthyOffline, DeviceID: deviceID, Hostname: hostname}
}

// A brand-new unhealthy signal fires exactly once: the notifier sees it in the
// digest's Opened section and the store records it opened + notified.
func TestReconcileFiresNewAlertOnce(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(offline("dev-a", "mac-a"))
	notifier := &fakeNotifier{}
	r := newReconciler(store, notifier, now)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	calls := notifier.calls()
	if len(calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1", len(calls))
	}
	if len(calls[0].Opened) != 1 || calls[0].Opened[0].DeviceID != "dev-a" {
		t.Fatalf("digest Opened = %+v", calls[0].Opened)
	}
	a, ok := store.get(alertKey{registry.UnhealthyOffline, "dev-a", ""})
	if !ok {
		t.Fatal("alert not opened in store")
	}
	if a.LastNotifiedAt == nil {
		t.Error("alert not marked notified")
	}
}

// A signal that stays unhealthy is notified only once — the second tick finds
// it already open + notified and produces no further notification.
func TestReconcileStillUnhealthyIsSilent(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(offline("dev-a", "mac-a"))
	notifier := &fakeNotifier{}
	r := newReconciler(store, notifier, now)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	// Same snapshot, next tick: nothing new.
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	if calls := notifier.calls(); len(calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1 (no repeat)", len(calls))
	}
}

// When a notified alert clears (gone from the snapshot), the next tick queues a
// recovery in the digest's Resolved section and resolves the alert_state row.
func TestReconcileResolvesClearedAlert(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(offline("dev-a", "mac-a"))
	notifier := &fakeNotifier{}
	r := newReconciler(store, notifier, now)

	// Tick 1: open + notify the offline alert.
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	// Device recovers: snapshot is now empty.
	store.setSnapshot()
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	calls := notifier.calls()
	if len(calls) != 2 {
		t.Fatalf("notifier calls = %d, want 2 (open + recover)", len(calls))
	}
	recover := calls[1]
	if len(recover.Resolved) != 1 || recover.Resolved[0].DeviceID != "dev-a" {
		t.Fatalf("recovery digest Resolved = %+v", recover.Resolved)
	}
	if len(recover.Opened) != 0 {
		t.Errorf("recovery digest should have no Opened, got %+v", recover.Opened)
	}
	if _, ok := store.get(alertKey{registry.UnhealthyOffline, "dev-a", ""}); ok {
		t.Error("alert should be resolved (no longer open)")
	}
}

// A delivery failure leaves the alert un-notified: opened_at is recorded so the
// detection time survives, but the next tick (with the notifier recovered)
// re-detects it as owed and retries the notification.
func TestReconcileRetriesAfterDeliveryFailure(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(offline("dev-a", "mac-a"))
	notifier := &fakeNotifier{failWith: errors.New("teams 503")}
	r := newReconciler(store, notifier, now)

	// Tick 1: notify fails.
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("first tick returned error (should be swallowed): %v", err)
	}
	a, ok := store.get(alertKey{registry.UnhealthyOffline, "dev-a", ""})
	if !ok {
		t.Fatal("alert should be opened even when delivery fails")
	}
	if a.LastNotifiedAt != nil {
		t.Error("alert must NOT be marked notified after a failed send")
	}

	// Tick 2: notifier recovers; the still-owed alert is retried.
	notifier.failWith = nil
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	calls := notifier.calls()
	if len(calls) != 2 {
		t.Fatalf("notifier calls = %d, want 2 (fail + retry)", len(calls))
	}
	if len(calls[1].Opened) != 1 || calls[1].Opened[0].DeviceID != "dev-a" {
		t.Fatalf("retry digest Opened = %+v", calls[1].Opened)
	}
	a, _ = store.get(alertKey{registry.UnhealthyOffline, "dev-a", ""})
	if a.LastNotifiedAt == nil {
		t.Error("alert should be marked notified after the successful retry")
	}
}

// Many new transitions in one tick coalesce into a single digest delivered
// once — a site-wide outage is one message, not one per device.
func TestReconcileCoalescesIntoOneDigest(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(
		offline("dev-a", "mac-a"),
		offline("dev-b", "mac-b"),
		registry.UnhealthySignal{Kind: registry.UnhealthyServiceStopped, DeviceID: "dev-c", Subject: "alpr", Hostname: "mac-c"},
	)
	notifier := &fakeNotifier{}
	r := newReconciler(store, notifier, now)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	calls := notifier.calls()
	if len(calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1 (coalesced)", len(calls))
	}
	if len(calls[0].Opened) != 3 {
		t.Fatalf("digest Opened = %d events, want 3", len(calls[0].Opened))
	}
}

// The per-tick cap bounds how many opened alerts are enumerated; the overflow
// is summarized as a Truncated count. Every alert is still opened + notified —
// truncation only limits the digest's enumeration, not the bookkeeping.
func TestReconcileCapsDigestButPersistsAll(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(
		offline("dev-a", "mac-a"),
		offline("dev-b", "mac-b"),
		offline("dev-c", "mac-c"),
	)
	notifier := &fakeNotifier{}
	r := ingest.NewNotificationReconciler(store, notifier, ingest.NotificationReconcilerConfig{
		Cap: 2,
		Now: func() time.Time { return now },
	})

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	calls := notifier.calls()
	if len(calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1", len(calls))
	}
	if len(calls[0].Opened) != 2 {
		t.Errorf("enumerated Opened = %d, want 2 (capped)", len(calls[0].Opened))
	}
	if calls[0].Truncated != 1 {
		t.Errorf("Truncated = %d, want 1", calls[0].Truncated)
	}
	// All three alerts must still be opened + notified despite the cap.
	for _, id := range []string{"dev-a", "dev-b", "dev-c"} {
		a, ok := store.get(alertKey{registry.UnhealthyOffline, id, ""})
		if !ok || a.LastNotifiedAt == nil {
			t.Errorf("%s not opened+notified (cap should not drop bookkeeping)", id)
		}
	}
}

// An alert whose open notice never delivered (the send failed) and which then
// clears before any retry is resolved silently — no recovery is announced for
// an alert nobody was told about.
func TestReconcileSilentlyResolvesNeverNotified(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(offline("dev-a", "mac-a"))
	notifier := &fakeNotifier{failWith: errors.New("teams down")}
	r := newReconciler(store, notifier, now)

	// Tick 1: open recorded, send fails (never notified).
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	// Device recovers before any successful send; notifier is healthy again.
	store.setSnapshot()
	notifier.failWith = nil
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	// The only notifier call was the failed tick-1 attempt; the silent resolve
	// sends nothing.
	if calls := notifier.calls(); len(calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1 (no recovery for never-notified)", len(calls))
	}
	if _, ok := store.get(alertKey{registry.UnhealthyOffline, "dev-a", ""}); ok {
		t.Error("never-notified alert should be silently resolved")
	}
}

// The reconciler reads channel config from the ConfigSource each tick and hands
// it to the notifier, so an operator's edit reaches delivery without a redeploy.
func TestReconcilePassesConfigToNotifier(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(offline("dev-a", "mac-a"))
	notifier := &fakeNotifier{}
	cs := &fakeConfigSource{}
	cs.set(ingest.NotificationConfig{
		Enabled: true,
		NotifyConfig: ingest.NotifyConfig{
			Recipients:      []string{"ops@example.com"},
			TeamsWebhookURL: "https://hook.example/x",
		},
	})
	r := newReconcilerWithConfig(store, notifier, cs, now)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	got := notifier.lastConfig()
	if len(got.Recipients) != 1 || got.Recipients[0] != "ops@example.com" {
		t.Errorf("recipients = %v", got.Recipients)
	}
	if got.TeamsWebhookURL != "https://hook.example/x" {
		t.Errorf("webhook = %q", got.TeamsWebhookURL)
	}
}

// When notifications are disabled, the reconciler sends nothing but still keeps
// alert_state accurate: an owed alert is opened (not marked notified, so it
// fires on re-enable) and a cleared alert is resolved with no recovery notice.
func TestReconcileDisabledSkipsSendButKeepsState(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeAlertStore()
	store.setSnapshot(offline("dev-a", "mac-a"))
	notifier := &fakeNotifier{}
	cs := &fakeConfigSource{}
	cs.set(ingest.NotificationConfig{Enabled: false})
	r := newReconcilerWithConfig(store, notifier, cs, now)

	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	if calls := notifier.calls(); len(calls) != 0 {
		t.Fatalf("notifier calls = %d, want 0 while disabled", len(calls))
	}
	a, ok := store.get(alertKey{registry.UnhealthyOffline, "dev-a", ""})
	if !ok {
		t.Fatal("owed alert should still be opened while disabled (bookkeeping)")
	}
	if a.LastNotifiedAt != nil {
		t.Error("alert must NOT be marked notified while disabled — it fires on re-enable")
	}

	// Re-enable: the still-open, never-notified alert now fires.
	cs.set(ingest.NotificationConfig{Enabled: true})
	if err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if calls := notifier.calls(); len(calls) != 1 {
		t.Fatalf("after re-enable notifier calls = %d, want 1", len(calls))
	}
}
