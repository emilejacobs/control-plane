package integration_test

import (
	"context"
	"testing"
)

// Issue #40 cycle 1: devices.desired_agent_version round-trips through
// SetDesiredAgentVersion + GetByID. NULL (nil) means "untargeted" — a fresh
// enrollment has no desired version until a rollout targets it.
func TestRegistryDesiredAgentVersionRoundTrip(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	devA := enrollForTest(t, srv, "mac-mini-roll-01", "40000000-0000-0000-0000-000000000001")
	devB := enrollForTest(t, srv, "mac-mini-roll-02", "40000000-0000-0000-0000-000000000002")
	scopedCtx := staffCtx(ctx)

	// Fresh devices are untargeted.
	dev, err := srv.Registry.GetByID(scopedCtx, devA)
	if err != nil {
		t.Fatalf("GetByID fresh: %v", err)
	}
	if dev.DesiredAgentVersion != nil {
		t.Errorf("fresh DesiredAgentVersion: got %v want nil", *dev.DesiredAgentVersion)
	}

	// Targeting a set stamps the version on every named device.
	n, err := srv.Registry.SetDesiredAgentVersion(ctx, []string{devA, devB}, "v1.4.0")
	if err != nil {
		t.Fatalf("SetDesiredAgentVersion: %v", err)
	}
	if n != 2 {
		t.Errorf("affected: got %d want 2", n)
	}
	for _, id := range []string{devA, devB} {
		dev, err := srv.Registry.GetByID(scopedCtx, id)
		if err != nil {
			t.Fatalf("GetByID %s: %v", id, err)
		}
		if dev.DesiredAgentVersion == nil || *dev.DesiredAgentVersion != "v1.4.0" {
			t.Errorf("DesiredAgentVersion %s: got %v want v1.4.0", id, dev.DesiredAgentVersion)
		}
	}

	// Re-targeting overwrites (last-wins) — canary promote / abort both
	// reduce to another set.
	if _, err := srv.Registry.SetDesiredAgentVersion(ctx, []string{devA}, "v1.5.0"); err != nil {
		t.Fatalf("SetDesiredAgentVersion (2nd): %v", err)
	}
	dev, err = srv.Registry.GetByID(scopedCtx, devA)
	if err != nil {
		t.Fatalf("GetByID after 2nd set: %v", err)
	}
	if dev.DesiredAgentVersion == nil || *dev.DesiredAgentVersion != "v1.5.0" {
		t.Errorf("DesiredAgentVersion after 2nd set: got %v want v1.5.0", dev.DesiredAgentVersion)
	}
}

// Unknown or non-UUID ids in the target set are skipped, not an error: the
// affected count is the caller's signal (the API layer 404s on count 0).
func TestRegistryDesiredAgentVersionUnknownIDsSkipped(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	devA := enrollForTest(t, srv, "mac-mini-roll-03", "40000000-0000-0000-0000-000000000003")

	n, err := srv.Registry.SetDesiredAgentVersion(
		ctx,
		[]string{devA, "11111111-2222-3333-4444-555555555555", "not-a-uuid"},
		"v1.4.0",
	)
	if err != nil {
		t.Fatalf("SetDesiredAgentVersion: %v", err)
	}
	if n != 1 {
		t.Errorf("affected: got %d want 1", n)
	}
}
