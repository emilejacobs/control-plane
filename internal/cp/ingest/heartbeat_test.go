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

// networkCall records every UpdateHeartbeatNetwork invocation so
// the cycle 5 tests can assert (a) it was called and (b) with the
// expected *string field values (including nil-means-omitted).
type networkCall struct {
	deviceID                       string
	lanIP, tailscaleIP, tailscaleN *string
}

// fakeWriter records every UpdateLastSeen call and returns its configured
// error.
type fakeWriter struct {
	err          error
	calls        []writeCall
	networkErr   error
	networkCalls []networkCall
}

func (f *fakeWriter) UpdateLastSeen(_ context.Context, deviceID string, at time.Time) error {
	f.calls = append(f.calls, writeCall{deviceID, at})
	return f.err
}

func (f *fakeWriter) UpdateHeartbeatNetwork(_ context.Context, deviceID string, lanIP, tailscaleIP, tailscaleName *string) error {
	f.networkCalls = append(f.networkCalls, networkCall{deviceID, lanIP, tailscaleIP, tailscaleName})
	return f.networkErr
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

// Issue #14 cycle 5: when the heartbeat envelope carries any of
// the three new network fields, the ingester calls
// UpdateHeartbeatNetwork with the present values and nil for the
// absent ones. The conditional-update semantics in the registry
// (COALESCE) keep stored values for a field the agent didn't
// publish on this tick.
func TestPresenceIngesterPersistsNetworkFields(t *testing.T) {
	at := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	p := presence.New()
	w := &fakeWriter{}
	ing := NewPresenceIngester(p, w, fixedClock(at))

	lanIP := "192.168.54.215"
	tailscaleIP := "100.122.190.107"
	tailscaleName := "07-eegees-store54-macmini.tailnet.ts.net"
	hb := Heartbeat{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		LanIP:         lanIP,
		TailscaleIP:   tailscaleIP,
		TailscaleName: tailscaleName,
	}
	if err := ing.Handle(context.Background(), hb); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.networkCalls) != 1 {
		t.Fatalf("UpdateHeartbeatNetwork calls: got %d want 1", len(w.networkCalls))
	}
	got := w.networkCalls[0]
	if got.deviceID != "dev-1" {
		t.Errorf("deviceID: got %q want dev-1", got.deviceID)
	}
	if got.lanIP == nil || *got.lanIP != lanIP {
		t.Errorf("lanIP: got %v want %q", got.lanIP, lanIP)
	}
	if got.tailscaleIP == nil || *got.tailscaleIP != tailscaleIP {
		t.Errorf("tailscaleIP: got %v want %q", got.tailscaleIP, tailscaleIP)
	}
	if got.tailscaleN == nil || *got.tailscaleN != tailscaleName {
		t.Errorf("tailscaleName: got %v want %q", got.tailscaleN, tailscaleName)
	}
}

// A heartbeat that omits all three fields (the agent's wire
// envelope is back-compatibly missing) must NOT call
// UpdateHeartbeatNetwork — there's nothing to write. Older agents
// keep working unchanged.
func TestPresenceIngesterSkipsNetworkWhenAllFieldsAbsent(t *testing.T) {
	p := presence.New()
	w := &fakeWriter{}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))

	if err := ing.Handle(context.Background(), Heartbeat{DeviceID: "dev-1", CorrelationID: "corr-1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.networkCalls) != 0 {
		t.Errorf("UpdateHeartbeatNetwork should NOT be called when all three network fields are empty; got %d calls: %+v", len(w.networkCalls), w.networkCalls)
	}
}

// A heartbeat that carries only lan_ip (the agent lost tailnet
// visibility but its primary RFC1918 is fine) must call
// UpdateHeartbeatNetwork with nil for the absent tailscale_*
// fields — so the registry's COALESCE preserves the last-known
// stored tailscale_name and the dashboard's Verify-angle button
// keeps working.
func TestPresenceIngesterPartialNetworkUsesNilForAbsentFields(t *testing.T) {
	p := presence.New()
	w := &fakeWriter{}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))

	hb := Heartbeat{DeviceID: "dev-1", CorrelationID: "corr-1", LanIP: "192.168.1.7"}
	if err := ing.Handle(context.Background(), hb); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.networkCalls) != 1 {
		t.Fatalf("UpdateHeartbeatNetwork calls: got %d want 1", len(w.networkCalls))
	}
	got := w.networkCalls[0]
	if got.lanIP == nil || *got.lanIP != "192.168.1.7" {
		t.Errorf("lanIP: got %v want 192.168.1.7", got.lanIP)
	}
	if got.tailscaleIP != nil {
		t.Errorf("tailscaleIP: got %v want nil (absent in envelope)", *got.tailscaleIP)
	}
	if got.tailscaleN != nil {
		t.Errorf("tailscaleName: got %v want nil (absent in envelope)", *got.tailscaleN)
	}
}
