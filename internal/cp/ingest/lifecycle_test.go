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

// fakePresenceWriter records every SetPresence call and returns its
// configured error.
type fakePresenceWriter struct {
	err   error
	calls []setPresenceCall
}

func (f *fakePresenceWriter) SetPresence(_ context.Context, deviceID string, online bool, at time.Time) error {
	f.calls = append(f.calls, setPresenceCall{deviceID, online, at})
	return f.err
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
