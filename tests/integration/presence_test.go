package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// TestRegistryUpdateLastSeen is Issue 07 cycle 2: Registry.UpdateLastSeen
// stamps devices.last_seen, GetByID surfaces it, and an id matching no row
// (or not a UUID at all) is reported as ErrDeviceNotFound.
func TestRegistryUpdateLastSeen(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	deviceID := enrollForTest(t, srv, "mac-mini-presence-01", "33333333-3333-3333-4444-555555555555")

	// A freshly enrolled device has never been seen.
	dev, err := srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if dev.LastSeen != nil {
		t.Errorf("fresh device last_seen: got %v want nil", dev.LastSeen)
	}

	// UpdateLastSeen stamps it, and GetByID reads it back.
	at := time.Date(2026, 5, 21, 12, 30, 0, 0, time.UTC)
	if err := srv.Registry.UpdateLastSeen(ctx, deviceID, at); err != nil {
		t.Fatalf("UpdateLastSeen: %v", err)
	}
	dev, err = srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if dev.LastSeen == nil {
		t.Fatalf("last_seen still nil after UpdateLastSeen")
	}
	if !dev.LastSeen.Equal(at) {
		t.Errorf("last_seen: got %v want %v", dev.LastSeen, at)
	}

	// An unknown but well-formed device id.
	unknownID := "00000000-0000-0000-0000-000000000000"
	if err := srv.Registry.UpdateLastSeen(ctx, unknownID, at); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("unknown device id: got %v want ErrDeviceNotFound", err)
	}

	// A device id that isn't a UUID — must not surface as a DB error.
	if err := srv.Registry.UpdateLastSeen(ctx, "not-a-uuid", at); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("non-uuid device id: got %v want ErrDeviceNotFound", err)
	}
}

// TestRegistrySetPresence is Issue 08 cycle 2: Registry.SetPresence stamps
// is_online + presence_changed_at, GetByID surfaces them, and an id matching
// no row is reported as ErrDeviceNotFound.
func TestRegistrySetPresence(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	deviceID := enrollForTest(t, srv, "mac-mini-presence-03", "66666666-6666-6666-4444-555555555555")

	// A freshly enrolled device is offline with no presence-change time.
	dev, err := srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if dev.IsOnline {
		t.Errorf("fresh device: is_online = true, want false")
	}
	if dev.PresenceChangedAt != nil {
		t.Errorf("fresh device: presence_changed_at = %v, want nil", dev.PresenceChangedAt)
	}

	// Flip online, then offline — both land in the row.
	onlineAt := time.Date(2026, 5, 21, 13, 0, 0, 0, time.UTC)
	if err := srv.Registry.SetPresence(ctx, deviceID, true, onlineAt); err != nil {
		t.Fatalf("SetPresence online: %v", err)
	}
	dev, err = srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID after online: %v", err)
	}
	if !dev.IsOnline {
		t.Errorf("is_online: got false want true")
	}
	if dev.PresenceChangedAt == nil || !dev.PresenceChangedAt.Equal(onlineAt) {
		t.Errorf("presence_changed_at: got %v want %v", dev.PresenceChangedAt, onlineAt)
	}

	offlineAt := onlineAt.Add(5 * time.Minute)
	if err := srv.Registry.SetPresence(ctx, deviceID, false, offlineAt); err != nil {
		t.Fatalf("SetPresence offline: %v", err)
	}
	dev, err = srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID after offline: %v", err)
	}
	if dev.IsOnline {
		t.Errorf("is_online: got true want false")
	}
	if dev.PresenceChangedAt == nil || !dev.PresenceChangedAt.Equal(offlineAt) {
		t.Errorf("presence_changed_at: got %v want %v", dev.PresenceChangedAt, offlineAt)
	}

	// An unknown row, and a non-UUID id.
	if err := srv.Registry.SetPresence(ctx, "00000000-0000-0000-0000-000000000000", true, offlineAt); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("unknown device id: got %v want ErrDeviceNotFound", err)
	}
	if err := srv.Registry.SetPresence(ctx, "not-a-uuid", true, offlineAt); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("non-uuid device id: got %v want ErrDeviceNotFound", err)
	}
}

// TestUpdateLastSeenBringsDeviceOnline is Issue 08 cycle 3: a heartbeat
// marks the device online and stamps presence_changed_at on the first one;
// a steady-state heartbeat keeps it online without disturbing that stamp.
func TestUpdateLastSeenBringsDeviceOnline(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	deviceID := enrollForTest(t, srv, "mac-mini-presence-04", "77777777-7777-7777-4444-555555555555")

	// First heartbeat: offline → online, presence_changed_at stamped.
	hb1 := time.Date(2026, 5, 21, 14, 0, 0, 0, time.UTC)
	if err := srv.Registry.UpdateLastSeen(ctx, deviceID, hb1); err != nil {
		t.Fatalf("UpdateLastSeen 1: %v", err)
	}
	dev, err := srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !dev.IsOnline {
		t.Errorf("after first heartbeat: is_online false, want true")
	}
	if dev.PresenceChangedAt == nil || !dev.PresenceChangedAt.Equal(hb1) {
		t.Errorf("presence_changed_at: got %v want %v", dev.PresenceChangedAt, hb1)
	}

	// Second heartbeat: still online, presence_changed_at must not move —
	// nothing transitioned.
	hb2 := hb1.Add(30 * time.Second)
	if err := srv.Registry.UpdateLastSeen(ctx, deviceID, hb2); err != nil {
		t.Fatalf("UpdateLastSeen 2: %v", err)
	}
	dev, err = srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !dev.IsOnline {
		t.Errorf("after second heartbeat: is_online false, want true")
	}
	if dev.PresenceChangedAt == nil || !dev.PresenceChangedAt.Equal(hb1) {
		t.Errorf("presence_changed_at moved on a steady-state heartbeat: got %v want %v", dev.PresenceChangedAt, hb1)
	}
	if dev.LastSeen == nil || !dev.LastSeen.Equal(hb2) {
		t.Errorf("last_seen: got %v want %v", dev.LastSeen, hb2)
	}
}

// TestPresenceSweeperMarksStaleDeviceOffline is Issue 08 cycle 9: the
// PresenceSweeper, wired to the real Registry, flips a stale device's
// is_online to false in Postgres (AC 1 — the sweeper backstop).
func TestPresenceSweeperMarksStaleDeviceOffline(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	deviceID := enrollForTest(t, srv, "mac-mini-presence-05", "99999999-9999-9999-4444-555555555555")

	// The device is online, with a heartbeat recorded in the in-memory model.
	t0 := time.Now().UTC()
	if err := srv.Registry.SetPresence(ctx, deviceID, true, t0); err != nil {
		t.Fatalf("seed online: %v", err)
	}
	p := presence.New()
	p.RecordHeartbeat(deviceID, t0)

	// Run the sweeper with a clock well past the freshness threshold.
	sweeper := ingest.NewPresenceSweeper(p, srv.Registry, ingest.SweeperConfig{
		Interval: 10 * time.Millisecond,
		Now:      func() time.Time { return t0.Add(presence.OnlineThreshold + time.Minute) },
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { sweeper.Run(runCtx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dev, err := srv.Registry.GetByID(ctx, deviceID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !dev.IsOnline {
			return // swept offline — pass
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("sweeper did not mark the stale device offline within 5s")
}

// TestDeviceGetReportsOnline is Issue 08 cycle 3: GET /devices/{id} returns
// the stored is_online column, decoupled from last_seen freshness, so the
// disconnect/sweeper path can show offline while last_seen is still recent.
func TestDeviceGetReportsOnline(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	deviceID := enrollForTest(t, srv, "mac-mini-presence-02", "44444444-4444-4444-4444-555555555555")
	token := mintAccessToken(t)

	get := func() (online bool, ago *int64) {
		t.Helper()
		resp := doDeviceGet(t, srv.URL, deviceID, token)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET device: got %d want 200; body=%s", resp.StatusCode, raw)
		}
		var out struct {
			IsOnline           bool   `json:"is_online"`
			LastSeenAgoSeconds *int64 `json:"last_seen_ago_seconds"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out.IsOnline, out.LastSeenAgoSeconds
	}

	// Never seen → offline, null ago-seconds.
	if online, ago := get(); online || ago != nil {
		t.Errorf("never-seen device: online=%v ago=%v want false/nil", online, ago)
	}

	// A heartbeat brings the device online; ago-seconds is small.
	if err := srv.Registry.UpdateLastSeen(ctx, deviceID, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateLastSeen: %v", err)
	}
	online, ago := get()
	if !online {
		t.Errorf("after heartbeat: is_online=false want true")
	}
	if ago == nil || *ago < 0 || *ago > 60 {
		t.Errorf("after heartbeat: last_seen_ago_seconds=%v want small non-negative", ago)
	}

	// The presence column is the source of truth: SetPresence(false) makes
	// the API report offline even though last_seen is still recent.
	if err := srv.Registry.SetPresence(ctx, deviceID, false, time.Now().UTC()); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}
	online, ago = get()
	if online {
		t.Errorf("after SetPresence(false): is_online=true want false")
	}
	if ago == nil || *ago < 0 || *ago > 60 {
		t.Errorf("last_seen_ago_seconds should still reflect the recent last_seen: got %v", ago)
	}
}
