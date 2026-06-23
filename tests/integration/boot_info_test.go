package integration_test

import (
	"context"
	"testing"
	"time"
)

// TestRecordBootInfoDetectsReboots is the #157 reboot-detection acceptance
// test (PRD .scratch/offline-reason-tracking): a heartbeat with a new boot_time
// inserts exactly one device_reboots row carrying the cause; a repeat heartbeat
// with the same boot_time inserts none; a later, changed boot_time records a
// second reboot and advances the device's stored boot state.
func TestRecordBootInfoDetectsReboots(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-boot-01", "66666666-6666-6666-6666-666666666666")

	boot1 := time.Date(2026, 6, 20, 17, 14, 22, 0, time.UTC)
	code5 := 5

	// First contact: records the boot (one row) + sets the device's boot state.
	if err := srv.Registry.RecordBootInfo(ctx, deviceID, boot1, "clean restart", &code5, time.Now()); err != nil {
		t.Fatalf("RecordBootInfo (first): %v", err)
	}
	if n := rebootRowCount(t, ctx, srv, deviceID); n != 1 {
		t.Fatalf("after first contact: device_reboots rows = %d, want 1", n)
	}

	// Same boot_time on the next heartbeat — same boot, not a reboot. No new row.
	if err := srv.Registry.RecordBootInfo(ctx, deviceID, boot1, "clean restart", &code5, time.Now()); err != nil {
		t.Fatalf("RecordBootInfo (repeat): %v", err)
	}
	if n := rebootRowCount(t, ctx, srv, deviceID); n != 1 {
		t.Errorf("after repeat boot_time: device_reboots rows = %d, want 1 (no new row)", n)
	}

	// A changed boot_time — the device rebooted. Second row, with the cause.
	boot2 := boot1.Add(48 * time.Hour)
	codeNeg := -71
	if err := srv.Registry.RecordBootInfo(ctx, deviceID, boot2, "thermal", &codeNeg, time.Now()); err != nil {
		t.Fatalf("RecordBootInfo (reboot): %v", err)
	}
	if n := rebootRowCount(t, ctx, srv, deviceID); n != 2 {
		t.Errorf("after changed boot_time: device_reboots rows = %d, want 2", n)
	}

	// The latest reboot row carries the new cause + code.
	var gotBoot time.Time
	var gotCause string
	var gotCode int
	if err := srv.Pool.QueryRow(ctx, `
		SELECT boot_time, shutdown_cause, shutdown_cause_code
		FROM device_reboots WHERE device_id = $1
		ORDER BY detected_at DESC LIMIT 1`, deviceID).Scan(&gotBoot, &gotCause, &gotCode); err != nil {
		t.Fatalf("query latest reboot: %v", err)
	}
	if !gotBoot.Equal(boot2) || gotCause != "thermal" || gotCode != -71 {
		t.Errorf("latest reboot row: got %v/%q/%d want %v/thermal/-71", gotBoot, gotCause, gotCode, boot2)
	}

	// The device's stored boot state advanced to the latest boot.
	var devBoot time.Time
	var devCause string
	if err := srv.Pool.QueryRow(ctx, `
		SELECT last_boot_time, last_shutdown_cause FROM devices WHERE id = $1`, deviceID).
		Scan(&devBoot, &devCause); err != nil {
		t.Fatalf("query device boot state: %v", err)
	}
	if !devBoot.Equal(boot2) || devCause != "thermal" {
		t.Errorf("device boot state: got %v/%q want %v/thermal", devBoot, devCause, boot2)
	}
}

// A heartbeat with no shutdown cause (code nil) still records the boot; the
// cause columns are left NULL rather than written empty.
func TestRecordBootInfoWithoutCause(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-boot-02", "77777777-7777-7777-7777-777777777777")

	boot := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	if err := srv.Registry.RecordBootInfo(ctx, deviceID, boot, "", nil, time.Now()); err != nil {
		t.Fatalf("RecordBootInfo: %v", err)
	}

	var cause *string
	var code *int
	if err := srv.Pool.QueryRow(ctx, `
		SELECT shutdown_cause, shutdown_cause_code
		FROM device_reboots WHERE device_id = $1`, deviceID).Scan(&cause, &code); err != nil {
		t.Fatalf("query reboot row: %v", err)
	}
	if cause != nil || code != nil {
		t.Errorf("absent cause should be NULL; got cause=%v code=%v", cause, code)
	}
}

func rebootRowCount(t *testing.T, ctx context.Context, srv *testServer, deviceID string) int {
	t.Helper()
	var n int
	if err := srv.Pool.QueryRow(ctx,
		`SELECT count(*) FROM device_reboots WHERE device_id = $1`, deviceID).Scan(&n); err != nil {
		t.Fatalf("count device_reboots: %v", err)
	}
	return n
}
