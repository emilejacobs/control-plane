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

type writeCall struct {
	deviceID string
	at       time.Time
}

// fakeWriter records every UpdateLastSeen call and returns its configured
// error.
type fakeWriter struct {
	err   error
	calls []writeCall
}

func (f *fakeWriter) UpdateLastSeen(_ context.Context, deviceID string, at time.Time) error {
	f.calls = append(f.calls, writeCall{deviceID, at})
	return f.err
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestPresenceIngesterHappyPath(t *testing.T) {
	at := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	p := presence.New()
	w := &fakeWriter{}
	ing := NewPresenceIngester(p, w, fixedClock(at))

	err := ing.Handle(context.Background(), Heartbeat{DeviceID: "dev-1", CorrelationID: "corr-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(w.calls) != 1 {
		t.Fatalf("UpdateLastSeen calls: got %d want 1", len(w.calls))
	}
	if w.calls[0].deviceID != "dev-1" || !w.calls[0].at.Equal(at) {
		t.Errorf("UpdateLastSeen call: got %+v want dev-1 / %v", w.calls[0], at)
	}

	seen, ok := p.LastSeen("dev-1")
	if !ok || !seen.Equal(at) {
		t.Errorf("presence last seen: got %v ok=%v want %v", seen, ok, at)
	}
}

func TestPresenceIngesterUnknownDeviceIsPoison(t *testing.T) {
	p := presence.New()
	w := &fakeWriter{err: registry.ErrDeviceNotFound}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), Heartbeat{DeviceID: "ghost", CorrelationID: "corr-1"})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("unknown device: got %v want a poison error", err)
	}
	// The DB write failed, so in-memory presence must not have been touched.
	if _, ok := p.LastSeen("ghost"); ok {
		t.Error("presence recorded a heartbeat for an unknown device")
	}
}

func TestPresenceIngesterEmptyDeviceIDIsPoison(t *testing.T) {
	p := presence.New()
	w := &fakeWriter{}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), Heartbeat{DeviceID: "", CorrelationID: "corr-1"})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("empty device_id: got %v want a poison error", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("UpdateLastSeen called %d times for an empty device_id; want 0", len(w.calls))
	}
}

func TestPresenceIngesterTransientErrorIsRetryable(t *testing.T) {
	p := presence.New()
	w := &fakeWriter{err: errors.New("connection reset")}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))

	err := ing.Handle(context.Background(), Heartbeat{DeviceID: "dev-1", CorrelationID: "corr-1"})
	if err == nil {
		t.Fatal("Handle: got nil, want a transient error")
	}
	if errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("transient write failure was marked poison: %v", err)
	}
}
