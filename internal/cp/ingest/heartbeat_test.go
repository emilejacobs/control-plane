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
	err             error
	calls           []writeCall
	networkErr      error
	networkCalls    []networkCall
	rolledBackErr   error
	rolledBackCalls []rolledBackCall
}

type rolledBackCall struct {
	deviceID string
	version  string
}

func (f *fakeWriter) UpdateLastSeen(_ context.Context, deviceID string, at time.Time) error {
	f.calls = append(f.calls, writeCall{deviceID, at})
	return f.err
}

func (f *fakeWriter) UpdateHeartbeatNetwork(_ context.Context, deviceID string, lanIP, tailscaleIP, tailscaleName *string) error {
	f.networkCalls = append(f.networkCalls, networkCall{deviceID, lanIP, tailscaleIP, tailscaleName})
	return f.networkErr
}

// RecordReportedAgentVersion satisfies LastSeenWriter for the pre-#40 tests;
// versionWriter overrides it with a recording version.
func (f *fakeWriter) RecordReportedAgentVersion(context.Context, string, string) (*string, error) {
	return nil, nil
}

func (f *fakeWriter) RecordRolledBackVersion(_ context.Context, deviceID, version string) error {
	f.rolledBackCalls = append(f.rolledBackCalls, rolledBackCall{deviceID, version})
	return f.rolledBackErr
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// A heartbeat carrying rolled_back_version persists it (issue #42 follow-up);
// an absent field skips the write so the prior value is left untouched.
func TestPresenceIngesterRecordsRolledBackVersion(t *testing.T) {
	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	w := &fakeWriter{}
	ing := NewPresenceIngester(presence.New(), w, fixedClock(at))

	if err := ing.Handle(context.Background(),
		Heartbeat{DeviceID: "dev-1", RolledBackVersion: "1.4.1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.rolledBackCalls) != 1 ||
		w.rolledBackCalls[0].deviceID != "dev-1" ||
		w.rolledBackCalls[0].version != "1.4.1" {
		t.Fatalf("RecordRolledBackVersion calls: got %+v want one {dev-1, 1.4.1}", w.rolledBackCalls)
	}

	// No rollback field → no write.
	w2 := &fakeWriter{}
	ing2 := NewPresenceIngester(presence.New(), w2, fixedClock(at))
	if err := ing2.Handle(context.Background(),
		Heartbeat{DeviceID: "dev-1", Version: "1.4.1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w2.rolledBackCalls) != 0 {
		t.Errorf("absent rolled_back_version should skip the write; got %+v", w2.rolledBackCalls)
	}
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

// versionCall records every RecordReportedAgentVersion invocation.
type versionCall struct {
	deviceID string
	version  string
}

// versionWriter extends fakeWriter with the issue-#40 reported-version
// persistence; desired is what RecordReportedAgentVersion hands back.
type versionWriter struct {
	fakeWriter
	versionCalls []versionCall
	desired      *string
	versionErr   error
}

func (f *versionWriter) RecordReportedAgentVersion(_ context.Context, deviceID, version string) (*string, error) {
	f.versionCalls = append(f.versionCalls, versionCall{deviceID, version})
	return f.desired, f.versionErr
}

// pushCall records every reconcile push.
type pushCall struct {
	deviceID, version, correlationID string
}

type fakePusher struct {
	calls []pushCall
	err   error
}

func (f *fakePusher) Push(_ context.Context, deviceID, version, correlationID string) error {
	f.calls = append(f.calls, pushCall{deviceID, version, correlationID})
	return f.err
}

// Issue #40: a heartbeat carrying the agent's version persists it (the
// reported side of desired-vs-reported). A device whose desired version
// matches gets no push.
func TestPresenceIngesterPersistsReportedVersionNoPushWhenConverged(t *testing.T) {
	desired := "v1.4.0"
	p := presence.New()
	w := &versionWriter{desired: &desired}
	push := &fakePusher{}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))
	ing.Updates = push

	hb := Heartbeat{DeviceID: "dev-1", CorrelationID: "corr-1", Version: "v1.4.0"}
	if err := ing.Handle(context.Background(), hb); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.versionCalls) != 1 || w.versionCalls[0] != (versionCall{"dev-1", "v1.4.0"}) {
		t.Errorf("version calls = %+v, want one dev-1/v1.4.0", w.versionCalls)
	}
	if len(push.calls) != 0 {
		t.Errorf("pushed despite converged version: %+v", push.calls)
	}
}

// Issue #40 reconcile: a heartbeat reporting a version != desired re-pushes
// agent.update — this is the convergence engine for devices that missed (or
// failed) the initial push.
func TestPresenceIngesterRepushesOnVersionMismatch(t *testing.T) {
	desired := "v1.5.0"
	p := presence.New()
	w := &versionWriter{desired: &desired}
	push := &fakePusher{}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))
	ing.Updates = push

	hb := Heartbeat{DeviceID: "dev-1", CorrelationID: "corr-7", Version: "v1.4.0"}
	if err := ing.Handle(context.Background(), hb); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(push.calls) != 1 || push.calls[0] != (pushCall{"dev-1", "v1.5.0", "corr-7"}) {
		t.Errorf("push calls = %+v, want one dev-1/v1.5.0/corr-7", push.calls)
	}
}

// An untargeted device (desired NULL) never gets a push, whatever it reports.
func TestPresenceIngesterNoPushWhenUntargeted(t *testing.T) {
	p := presence.New()
	w := &versionWriter{desired: nil}
	push := &fakePusher{}
	ing := NewPresenceIngester(p, w, fixedClock(time.Now()))
	ing.Updates = push

	hb := Heartbeat{DeviceID: "dev-1", CorrelationID: "corr-1", Version: "v1.4.0"}
	if err := ing.Handle(context.Background(), hb); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(push.calls) != 0 {
		t.Errorf("pushed an untargeted device: %+v", push.calls)
	}
}

// A push failure must not fail heartbeat processing (the next heartbeat
// retries); a heartbeat without a version (pre-#40 agent) skips the
// version path entirely; and with no pusher configured (cp-ingest without
// AGENT_DIST_BUCKET) the version still persists.
func TestPresenceIngesterVersionEdgeCases(t *testing.T) {
	desired := "v1.5.0"

	t.Run("push failure is swallowed", func(t *testing.T) {
		w := &versionWriter{desired: &desired}
		push := &fakePusher{err: errors.New("iot down")}
		ing := NewPresenceIngester(presence.New(), w, fixedClock(time.Now()))
		ing.Updates = push
		if err := ing.Handle(context.Background(), Heartbeat{DeviceID: "dev-1", CorrelationID: "c", Version: "v1.4.0"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	})

	t.Run("no version in heartbeat", func(t *testing.T) {
		w := &versionWriter{desired: &desired}
		push := &fakePusher{}
		ing := NewPresenceIngester(presence.New(), w, fixedClock(time.Now()))
		ing.Updates = push
		if err := ing.Handle(context.Background(), Heartbeat{DeviceID: "dev-1", CorrelationID: "c"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if len(w.versionCalls) != 0 || len(push.calls) != 0 {
			t.Errorf("version path ran without a version: %+v %+v", w.versionCalls, push.calls)
		}
	})

	t.Run("nil pusher still persists version", func(t *testing.T) {
		w := &versionWriter{desired: &desired}
		ing := NewPresenceIngester(presence.New(), w, fixedClock(time.Now()))
		if err := ing.Handle(context.Background(), Heartbeat{DeviceID: "dev-1", CorrelationID: "c", Version: "v1.4.0"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if len(w.versionCalls) != 1 {
			t.Errorf("version not persisted with nil pusher: %+v", w.versionCalls)
		}
	})
}
