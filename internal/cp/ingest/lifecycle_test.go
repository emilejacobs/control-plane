package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
)

type setPresenceCall struct {
	deviceID string
	online   bool
	at       time.Time
}

type reconcileCall struct {
	staleBefore time.Time
	now         time.Time
}

// fakePresenceWriter records every SetPresence + ReconcileStalePresence call
// and returns its configured error / count.
type fakePresenceWriter struct {
	err            error
	calls          []setPresenceCall
	reconcileCalls []reconcileCall
	reconcileN     int
	reconcileErr   error
}

func (f *fakePresenceWriter) SetPresence(_ context.Context, deviceID string, online bool, at time.Time) error {
	f.calls = append(f.calls, setPresenceCall{deviceID, online, at})
	return f.err
}

func (f *fakePresenceWriter) ReconcileStalePresence(_ context.Context, staleBefore, now time.Time) (int, error) {
	f.reconcileCalls = append(f.reconcileCalls, reconcileCall{staleBefore, now})
	return f.reconcileN, f.reconcileErr
}

func TestLifecycleIngesterConnected(t *testing.T) {
	at := time.Date(2026, 5, 21, 15, 0, 0, 0, time.UTC)
	p := presence.New()
	w := &fakePresenceWriter{}
	ing := NewLifecycleIngester(p, w, fixedClock(at))

	err := ing.Handle(context.Background(), Lifecycle{
		ClientID: "dev-1", EventType: "connected", CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.calls) != 1 || w.calls[0] != (setPresenceCall{"dev-1", true, at}) {
		t.Errorf("SetPresence calls: got %+v want one {dev-1 true %v}", w.calls, at)
	}
	// The in-memory model was updated too (OnConnect refreshes last_seen).
	if seen, ok := p.LastSeen("dev-1"); !ok || !seen.Equal(at) {
		t.Errorf("in-memory last seen: got %v ok=%v want %v", seen, ok, at)
	}
}

func TestLifecycleIngesterDisconnected(t *testing.T) {
	at := time.Date(2026, 5, 21, 15, 0, 0, 0, time.UTC)
	p := presence.New()
	w := &fakePresenceWriter{}
	ing := NewLifecycleIngester(p, w, fixedClock(at))

	err := ing.Handle(context.Background(), Lifecycle{
		ClientID: "dev-1", EventType: "disconnected", CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.calls) != 1 || w.calls[0] != (setPresenceCall{"dev-1", false, at}) {
		t.Errorf("SetPresence calls: got %+v want one {dev-1 false %v}", w.calls, at)
	}
}

func TestLifecycleIngesterUnknownDeviceIsPoison(t *testing.T) {
	p := presence.New()
	w := &fakePresenceWriter{err: registry.ErrDeviceNotFound}
	ing := NewLifecycleIngester(p, w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), Lifecycle{
		ClientID: "ghost", EventType: "connected", CorrelationID: "corr-1",
	})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("unknown device: got %v want a poison error", err)
	}
	// The persist failed, so the in-memory model must not have been touched.
	if _, ok := p.LastSeen("ghost"); ok {
		t.Error("in-memory presence recorded an unknown device")
	}
}

func TestLifecycleIngesterUnknownEventTypeIsPoison(t *testing.T) {
	p := presence.New()
	w := &fakePresenceWriter{}
	ing := NewLifecycleIngester(p, w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), Lifecycle{
		ClientID: "dev-1", EventType: "rebooted", CorrelationID: "corr-1",
	})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("unknown eventType: got %v want a poison error", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("SetPresence called %d times for an unknown eventType; want 0", len(w.calls))
	}
}

func TestLifecycleIngesterEmptyClientIDIsPoison(t *testing.T) {
	p := presence.New()
	w := &fakePresenceWriter{}
	ing := NewLifecycleIngester(p, w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), Lifecycle{
		ClientID: "", EventType: "connected", CorrelationID: "corr-1",
	})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("empty clientId: got %v want a poison error", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("SetPresence called %d times for an empty clientId; want 0", len(w.calls))
	}
}

func TestLifecycleIngesterTransientErrorIsRetryable(t *testing.T) {
	p := presence.New()
	w := &fakePresenceWriter{err: errors.New("connection reset")}
	ing := NewLifecycleIngester(p, w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), Lifecycle{
		ClientID: "dev-1", EventType: "disconnected", CorrelationID: "corr-1",
	})
	if err == nil {
		t.Fatal("Handle: got nil, want a transient error")
	}
	if errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("transient write failure was marked poison: %v", err)
	}
}

// fakeVersionReader stubs the desired/reported lookup for reconcile.
type fakeVersionReader struct {
	reported string
	desired  *string
	err      error
	calls    int
}

func (f *fakeVersionReader) AgentVersionState(context.Context, string) (string, *string, error) {
	f.calls++
	return f.reported, f.desired, f.err
}

// Issue #40 reconcile: a device that reconnects still on the wrong version
// gets agent.update re-pushed — this is how an offline device converges on a
// rollout it missed.
func TestLifecycleIngesterRepushesOnReconnectMismatch(t *testing.T) {
	desired := "v1.5.0"
	w := &fakePresenceWriter{}
	versions := &fakeVersionReader{reported: "v1.4.0", desired: &desired}
	push := &fakePusher{}
	ing := NewLifecycleIngester(presence.New(), w, fixedClock(time.Now()))
	ing.Versions = versions
	ing.Updates = push

	err := ing.Handle(context.Background(), Lifecycle{
		ClientID: "dev-1", EventType: "connected", CorrelationID: "corr-3",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(push.calls) != 1 || push.calls[0] != (pushCall{"dev-1", "v1.5.0", "corr-3"}) {
		t.Errorf("push calls = %+v, want one dev-1/v1.5.0/corr-3", push.calls)
	}
}

// Cooldown: a device that keeps reconnecting still reporting the stale version
// (the loop signature: it restarts on each update before it can heartbeat the
// new version) is re-pushed at most once per cooldown window — not on every
// reconnect. This is what breaks the agent.update restart loop.
func TestLifecycleIngesterReconcileCooldown(t *testing.T) {
	desired := "v1.5.0"
	now := time.Now()
	clock := func() time.Time { return now }
	// reported stays stale across every reconnect — the device never gets to
	// report convergence, exactly the loop case.
	versions := &fakeVersionReader{reported: "v1.4.0", desired: &desired}
	push := &fakePusher{}
	ing := NewLifecycleIngester(presence.New(), &fakePresenceWriter{}, clock)
	ing.Versions = versions
	ing.Updates = push
	ing.ReconcileCooldown = time.Minute

	connect := func() {
		if err := ing.Handle(context.Background(), Lifecycle{
			ClientID: "dev-1", EventType: "connected", CorrelationID: "c",
		}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	connect() // first reconnect → pushes
	connect() // immediate reconnect → within cooldown → suppressed
	connect()
	if len(push.calls) != 1 {
		t.Fatalf("rapid reconnects: pushes = %d, want 1 (cooldown suppresses the rest)", len(push.calls))
	}

	// Once the cooldown elapses and the device is still stale, push again.
	now = now.Add(time.Minute + time.Second)
	connect()
	if len(push.calls) != 2 {
		t.Fatalf("after cooldown elapsed: pushes = %d, want 2", len(push.calls))
	}
}

// No push when the reconnecting device already runs the desired version, is
// untargeted, or the event is a disconnect.
func TestLifecycleIngesterNoPushCases(t *testing.T) {
	desired := "v1.4.0"

	t.Run("converged", func(t *testing.T) {
		push := &fakePusher{}
		ing := NewLifecycleIngester(presence.New(), &fakePresenceWriter{}, fixedClock(time.Now()))
		ing.Versions = &fakeVersionReader{reported: "v1.4.0", desired: &desired}
		ing.Updates = push
		if err := ing.Handle(context.Background(), Lifecycle{ClientID: "dev-1", EventType: "connected", CorrelationID: "c"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if len(push.calls) != 0 {
			t.Errorf("pushed a converged device: %+v", push.calls)
		}
	})

	t.Run("untargeted", func(t *testing.T) {
		push := &fakePusher{}
		ing := NewLifecycleIngester(presence.New(), &fakePresenceWriter{}, fixedClock(time.Now()))
		ing.Versions = &fakeVersionReader{reported: "v1.4.0", desired: nil}
		ing.Updates = push
		if err := ing.Handle(context.Background(), Lifecycle{ClientID: "dev-1", EventType: "connected", CorrelationID: "c"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if len(push.calls) != 0 {
			t.Errorf("pushed an untargeted device: %+v", push.calls)
		}
	})

	t.Run("disconnect skips the lookup entirely", func(t *testing.T) {
		push := &fakePusher{}
		versions := &fakeVersionReader{reported: "v1.0.0", desired: &desired}
		ing := NewLifecycleIngester(presence.New(), &fakePresenceWriter{}, fixedClock(time.Now()))
		ing.Versions = versions
		ing.Updates = push
		if err := ing.Handle(context.Background(), Lifecycle{ClientID: "dev-1", EventType: "disconnected", CorrelationID: "c"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if versions.calls != 0 || len(push.calls) != 0 {
			t.Errorf("disconnect triggered reconcile: lookups=%d pushes=%+v", versions.calls, push.calls)
		}
	})
}

// Reconcile failures (lookup or push) never fail the lifecycle event — the
// presence flip already persisted, and the device's own heartbeats retry the
// reconcile path.
func TestLifecycleIngesterReconcileFailuresAreSwallowed(t *testing.T) {
	desired := "v1.5.0"

	t.Run("lookup error", func(t *testing.T) {
		ing := NewLifecycleIngester(presence.New(), &fakePresenceWriter{}, fixedClock(time.Now()))
		ing.Versions = &fakeVersionReader{err: errors.New("db hiccup")}
		ing.Updates = &fakePusher{}
		if err := ing.Handle(context.Background(), Lifecycle{ClientID: "dev-1", EventType: "connected", CorrelationID: "c"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	})

	t.Run("push error", func(t *testing.T) {
		ing := NewLifecycleIngester(presence.New(), &fakePresenceWriter{}, fixedClock(time.Now()))
		ing.Versions = &fakeVersionReader{reported: "v1.4.0", desired: &desired}
		ing.Updates = &fakePusher{err: errors.New("iot down")}
		if err := ing.Handle(context.Background(), Lifecycle{ClientID: "dev-1", EventType: "connected", CorrelationID: "c"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	})
}
