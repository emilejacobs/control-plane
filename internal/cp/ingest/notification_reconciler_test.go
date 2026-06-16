package ingest_test

import (
	"context"
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
	failWith error
}

func (n *fakeNotifier) Notify(_ context.Context, d ingest.Digest) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.digests = append(n.digests, d)
	return n.failWith
}

func (n *fakeNotifier) calls() []ingest.Digest {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]ingest.Digest(nil), n.digests...)
}

func newReconciler(s ingest.NotificationStore, n ingest.Notifier, now time.Time) *ingest.NotificationReconciler {
	return ingest.NewNotificationReconciler(s, n, ingest.NotificationReconcilerConfig{
		Now: func() time.Time { return now },
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
