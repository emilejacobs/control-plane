package integration_test

import (
	"context"
	"testing"
)

// strPtr is a tiny helper for the *string fields the conditional
// UpdateHeartbeatNetwork takes.
func strPtr(s string) *string { return &s }

// Issue #14 cycle 4: the three new device columns
// (lan_ip, tailscale_ip, tailscale_name) round-trip through
// UpdateHeartbeatNetwork + GetByID. A subsequent call with different
// values overwrites (last-wins).
//
// Conditional-field semantics: a nil pointer for a column means
// "don't touch the stored value" — so an agent that intermittently
// loses tailnet visibility doesn't blow away the previously stored
// tailscale_* fields. Verified explicitly in
// TestRegistryHeartbeatNetwork_NilFieldsPreserveStored.
func TestRegistryHeartbeatNetworkRoundTrip(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-net-01", "14000000-0000-0000-0000-000000000001")
	scopedCtx := staffCtx(ctx)

	// Pre-rollout: a fresh device row has NULL for all three.
	dev, err := srv.Registry.GetByID(scopedCtx, deviceID)
	if err != nil {
		t.Fatalf("GetByID fresh: %v", err)
	}
	if dev.LanIP != nil {
		t.Errorf("fresh LanIP: got %v want nil", *dev.LanIP)
	}
	if dev.TailscaleIP != nil {
		t.Errorf("fresh TailscaleIP: got %v want nil", *dev.TailscaleIP)
	}
	if dev.TailscaleName != nil {
		t.Errorf("fresh TailscaleName: got %v want nil", *dev.TailscaleName)
	}

	// First heartbeat with all three fields populated.
	if err := srv.Registry.UpdateHeartbeatNetwork(
		ctx, deviceID,
		strPtr("192.168.54.215"),
		strPtr("100.122.190.107"),
		strPtr("07-eegees-store54-macmini.tailnet.ts.net"),
	); err != nil {
		t.Fatalf("UpdateHeartbeatNetwork: %v", err)
	}

	dev, err = srv.Registry.GetByID(scopedCtx, deviceID)
	if err != nil {
		t.Fatalf("GetByID after write: %v", err)
	}
	if dev.LanIP == nil || *dev.LanIP != "192.168.54.215" {
		t.Errorf("LanIP: got %v want 192.168.54.215", dev.LanIP)
	}
	if dev.TailscaleIP == nil || *dev.TailscaleIP != "100.122.190.107" {
		t.Errorf("TailscaleIP: got %v want 100.122.190.107", dev.TailscaleIP)
	}
	if dev.TailscaleName == nil || *dev.TailscaleName != "07-eegees-store54-macmini.tailnet.ts.net" {
		t.Errorf("TailscaleName: got %v want 07-eegees-store54-macmini.tailnet.ts.net", dev.TailscaleName)
	}

	// Second heartbeat with new values — last-wins.
	if err := srv.Registry.UpdateHeartbeatNetwork(
		ctx, deviceID,
		strPtr("10.1.2.3"),
		strPtr("100.99.0.7"),
		strPtr("renamed.tailnet.ts.net"),
	); err != nil {
		t.Fatalf("UpdateHeartbeatNetwork (2nd): %v", err)
	}
	dev, err = srv.Registry.GetByID(scopedCtx, deviceID)
	if err != nil {
		t.Fatalf("GetByID after 2nd write: %v", err)
	}
	if dev.LanIP == nil || *dev.LanIP != "10.1.2.3" {
		t.Errorf("LanIP (2nd): got %v want 10.1.2.3", dev.LanIP)
	}
	if dev.TailscaleIP == nil || *dev.TailscaleIP != "100.99.0.7" {
		t.Errorf("TailscaleIP (2nd): got %v want 100.99.0.7", dev.TailscaleIP)
	}
	if dev.TailscaleName == nil || *dev.TailscaleName != "renamed.tailnet.ts.net" {
		t.Errorf("TailscaleName (2nd): got %v want renamed.tailnet.ts.net", dev.TailscaleName)
	}
}

// A heartbeat that omits a field (nil pointer in the registry
// call) must NOT NULL out the previously stored value. This is the
// "agent temporarily lost tailnet visibility" case — we want the
// dashboard to keep showing the last known tailscale_name until a
// later heartbeat lands a new value.
func TestRegistryHeartbeatNetwork_NilFieldsPreserveStored(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-net-02", "14000000-0000-0000-0000-000000000002")
	scopedCtx := staffCtx(ctx)

	// First heartbeat: all three set.
	if err := srv.Registry.UpdateHeartbeatNetwork(
		ctx, deviceID,
		strPtr("192.168.1.100"),
		strPtr("100.64.0.42"),
		strPtr("first.tailnet.ts.net"),
	); err != nil {
		t.Fatalf("UpdateHeartbeatNetwork: %v", err)
	}

	// Second heartbeat: only lan_ip changed; tailscale_* nil.
	if err := srv.Registry.UpdateHeartbeatNetwork(
		ctx, deviceID,
		strPtr("192.168.1.250"),
		nil, // tailnet was unreachable this tick — don't clobber.
		nil,
	); err != nil {
		t.Fatalf("UpdateHeartbeatNetwork (partial): %v", err)
	}

	dev, err := srv.Registry.GetByID(scopedCtx, deviceID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if dev.LanIP == nil || *dev.LanIP != "192.168.1.250" {
		t.Errorf("LanIP: got %v want 192.168.1.250", dev.LanIP)
	}
	if dev.TailscaleIP == nil || *dev.TailscaleIP != "100.64.0.42" {
		t.Errorf("TailscaleIP should be preserved from prior heartbeat: got %v want 100.64.0.42", dev.TailscaleIP)
	}
	if dev.TailscaleName == nil || *dev.TailscaleName != "first.tailnet.ts.net" {
		t.Errorf("TailscaleName should be preserved: got %v want first.tailnet.ts.net", dev.TailscaleName)
	}
}
