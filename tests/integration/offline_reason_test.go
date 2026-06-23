package integration_test

import (
	"context"
	"testing"
	"time"
)

// TestOfflineReasonClassifiesRecovery is the #158 acceptance test: over an
// offline window, a boot_time change in the window yields "reboot: <cause>", an
// unchanged boot_time (a device that does report boot info) yields "network
// blip", and a device that never reported boot info yields "" (unknown — a
// pre-#157 agent degrades gracefully, no false reason).
func TestOfflineReasonClassifiesRecovery(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	devA := enrollForTest(t, srv, "mac-mini-reason-a", "88888888-8888-8888-8888-888888888888")
	devB := enrollForTest(t, srv, "mac-mini-reason-b", "99999999-9999-9999-9999-999999999999")

	t0 := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(30 * time.Minute)
	t2 := t0.Add(60 * time.Minute)

	boot1 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	boot2 := boot1.Add(48 * time.Hour)
	code5, codeNeg := 5, -71

	// devA: first contact at t0 (boot1), then a reboot detected at t1 (boot2/thermal).
	if err := srv.Registry.RecordBootInfo(ctx, devA, boot1, "clean restart", &code5, t0); err != nil {
		t.Fatalf("RecordBootInfo A boot1: %v", err)
	}
	if err := srv.Registry.RecordBootInfo(ctx, devA, boot2, "thermal", &codeNeg, t1); err != nil {
		t.Fatalf("RecordBootInfo A boot2: %v", err)
	}

	// Window [t0+15m, t2] includes the t1 reboot → reboot reason with the cause.
	reason, err := srv.Registry.OfflineReason(ctx, devA, t0.Add(15*time.Minute), t2)
	if err != nil {
		t.Fatalf("OfflineReason A (reboot window): %v", err)
	}
	if reason != "reboot: thermal" {
		t.Errorf("reboot window: got %q want 'reboot: thermal'", reason)
	}

	// Window [t1+15m, t2] excludes every reboot, but devA reports boot info →
	// genuine network blip.
	reason, err = srv.Registry.OfflineReason(ctx, devA, t1.Add(15*time.Minute), t2)
	if err != nil {
		t.Fatalf("OfflineReason A (blip window): %v", err)
	}
	if reason != "network blip" {
		t.Errorf("blip window: got %q want 'network blip'", reason)
	}

	// devB never reported boot info → unknown (empty), not a false "network blip".
	reason, err = srv.Registry.OfflineReason(ctx, devB, t0, t2)
	if err != nil {
		t.Fatalf("OfflineReason B (unknown): %v", err)
	}
	if reason != "" {
		t.Errorf("old-agent device: got %q want '' (unknown)", reason)
	}
}

// A reboot with no shutdown cause (the log read failed on the device) still
// classifies as a reboot — just without the ": <cause>" suffix.
func TestOfflineReasonRebootWithoutCause(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	dev := enrollForTest(t, srv, "mac-mini-reason-c", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

	t0 := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	boot := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	if err := srv.Registry.RecordBootInfo(ctx, dev, boot, "", nil, t0); err != nil {
		t.Fatalf("RecordBootInfo: %v", err)
	}

	reason, err := srv.Registry.OfflineReason(ctx, dev, t0.Add(-time.Minute), t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("OfflineReason: %v", err)
	}
	if reason != "reboot" {
		t.Errorf("got %q want 'reboot' (no cause suffix)", reason)
	}
}
