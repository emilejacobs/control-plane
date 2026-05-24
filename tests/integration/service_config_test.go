package integration_test

import (
	"context"
	"testing"
)

// TestRegistryServiceConfigRoundTrip — Phase 2 slice 2, storage cycle.
// SetServiceConfig persists per-device override of the service allow-list
// and cadence; GetServiceConfig round-trips every field. Three shapes
// matter:
//
//  1. No override at all (post-enrollment default state): both fields nil.
//  2. Override with a non-empty allow-list + a non-default interval.
//  3. Explicit empty allow-list ([]) — different from nil (means
//     "track nothing"), per PRD § Decisions / "Override semantics".
//
// All three must round-trip without nil/[] confusion in the JSONB column.
func TestRegistryServiceConfigRoundTrip(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cfg-01", "11111111-2222-3333-4444-aaaaaaaaaaaa")
	ctx = staffCtx(ctx) // GetServiceConfig is site-scoped (ADR-012 gate); test uses staff scope

	// 1. Fresh device: no override on either field.
	cfg, err := srv.Registry.GetServiceConfig(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetServiceConfig (fresh): %v", err)
	}
	if cfg.AllowListOverride != nil {
		t.Errorf("fresh device AllowListOverride: got %v, want nil", *cfg.AllowListOverride)
	}
	if cfg.IntervalOverride != nil {
		t.Errorf("fresh device IntervalOverride: got %q, want nil", *cfg.IntervalOverride)
	}

	// 2. Set both: non-empty list + custom interval.
	list := []string{"com.uknomi.webui", "com.tailscale.tailscaled", "anydesk"}
	interval := "2m"
	if err := srv.Registry.SetServiceConfig(ctx, deviceID, &list, &interval); err != nil {
		t.Fatalf("SetServiceConfig (set): %v", err)
	}
	cfg, err = srv.Registry.GetServiceConfig(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetServiceConfig (after set): %v", err)
	}
	if cfg.AllowListOverride == nil {
		t.Fatal("AllowListOverride: got nil, want list")
	}
	if got, want := *cfg.AllowListOverride, list; !equalStrings(got, want) {
		t.Errorf("AllowListOverride: got %v, want %v", got, want)
	}
	if cfg.IntervalOverride == nil || *cfg.IntervalOverride != "2m" {
		t.Errorf("IntervalOverride: got %v, want %q", cfg.IntervalOverride, "2m")
	}

	// 3. Empty list ([]) is a meaningful override ("track nothing"),
	// distinct from nil. The JSONB column stores `[]` literally.
	empty := []string{}
	if err := srv.Registry.SetServiceConfig(ctx, deviceID, &empty, nil); err != nil {
		t.Fatalf("SetServiceConfig (empty list, clear interval): %v", err)
	}
	cfg, err = srv.Registry.GetServiceConfig(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetServiceConfig (after empty): %v", err)
	}
	if cfg.AllowListOverride == nil {
		t.Fatal("AllowListOverride after explicit empty: got nil, want []")
	}
	if got := *cfg.AllowListOverride; len(got) != 0 {
		t.Errorf("AllowListOverride after explicit empty: got %v, want []", got)
	}
	if cfg.IntervalOverride != nil {
		t.Errorf("IntervalOverride after nil clear: got %q, want nil", *cfg.IntervalOverride)
	}

	// 4. Clear both with nils.
	if err := srv.Registry.SetServiceConfig(ctx, deviceID, nil, nil); err != nil {
		t.Fatalf("SetServiceConfig (clear both): %v", err)
	}
	cfg, err = srv.Registry.GetServiceConfig(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetServiceConfig (after clear): %v", err)
	}
	if cfg.AllowListOverride != nil {
		t.Errorf("AllowListOverride after nil clear: got %v, want nil", *cfg.AllowListOverride)
	}
	if cfg.IntervalOverride != nil {
		t.Errorf("IntervalOverride after nil clear: got %q, want nil", *cfg.IntervalOverride)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
