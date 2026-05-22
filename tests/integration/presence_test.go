package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

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

// TestDeviceGetReportsOnline is Issue 07 cycle 3: GET /devices/{id} derives
// is_online and last_seen_ago_seconds from the last_seen column against the
// 90s threshold.
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

	// Seen just now → online.
	if err := srv.Registry.UpdateLastSeen(ctx, deviceID, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateLastSeen now: %v", err)
	}
	online, ago := get()
	if !online {
		t.Errorf("just-seen device: is_online=false want true")
	}
	if ago == nil || *ago < 0 || *ago > 60 {
		t.Errorf("just-seen device: last_seen_ago_seconds=%v want small non-negative", ago)
	}

	// Seen well past the 90s threshold → offline, with a real ago-seconds.
	stale := time.Now().UTC().Add(-(presence.OnlineThreshold + 30*time.Second))
	if err := srv.Registry.UpdateLastSeen(ctx, deviceID, stale); err != nil {
		t.Fatalf("UpdateLastSeen stale: %v", err)
	}
	online, ago = get()
	if online {
		t.Errorf("stale device: is_online=true want false")
	}
	if ago == nil || *ago < 110 {
		t.Errorf("stale device: last_seen_ago_seconds=%v want >= 110", ago)
	}
}
