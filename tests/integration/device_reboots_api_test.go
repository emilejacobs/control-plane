package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestGetDeviceSurfacesBootStateAndReboots is the #159 acceptance test: the
// device GET surfaces last boot time + last shutdown cause, and a recent-reboots
// history list (newest first, with cause), once the device has reported boot
// info. A device that never reported it gets null fields + an empty list — the
// graceful empty state for an old agent.
func TestGetDeviceSurfacesBootStateAndReboots(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	withBoot := enrollForTest(t, srv, "mac-mini-reboot-ui", "12121212-1212-1212-1212-121212121212")
	noBoot := enrollForTest(t, srv, "mac-mini-noboot-ui", "13131313-1313-1313-1313-131313131313")

	t0 := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	boot1 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	boot2 := boot1.Add(48 * time.Hour)
	code5, codeNeg := 5, -71
	if err := srv.Registry.RecordBootInfo(ctx, withBoot, boot1, "clean restart", &code5, t0); err != nil {
		t.Fatalf("RecordBootInfo boot1: %v", err)
	}
	if err := srv.Registry.RecordBootInfo(ctx, withBoot, boot2, "thermal", &codeNeg, t0.Add(time.Hour)); err != nil {
		t.Fatalf("RecordBootInfo boot2: %v", err)
	}

	token := mintAccessToken(t, ctx, srv)

	// Device that reported boot info: fields populated, two reboots newest-first.
	got := decodeDeviceBoot(t, srv.URL, withBoot, token)
	if got.LastBootTime == nil || *got.LastBootTime != boot2.Format(time.RFC3339) {
		t.Errorf("last_boot_time: got %v want %s", got.LastBootTime, boot2.Format(time.RFC3339))
	}
	if got.LastShutdownCause == nil || *got.LastShutdownCause != "thermal" {
		t.Errorf("last_shutdown_cause: got %v want thermal", got.LastShutdownCause)
	}
	if got.LastShutdownCauseCode == nil || *got.LastShutdownCauseCode != -71 {
		t.Errorf("last_shutdown_cause_code: got %v want -71", got.LastShutdownCauseCode)
	}
	if len(got.RecentReboots) != 2 {
		t.Fatalf("recent_reboots: got %d want 2", len(got.RecentReboots))
	}
	if got.RecentReboots[0].BootTime != boot2.Format(time.RFC3339) || got.RecentReboots[0].ShutdownCause == nil || *got.RecentReboots[0].ShutdownCause != "thermal" {
		t.Errorf("newest reboot: got %+v want boot2/thermal", got.RecentReboots[0])
	}
	if got.RecentReboots[1].BootTime != boot1.Format(time.RFC3339) {
		t.Errorf("oldest reboot boot_time: got %q want %s", got.RecentReboots[1].BootTime, boot1.Format(time.RFC3339))
	}

	// Device with no boot info: null fields, empty (non-null) list.
	none := decodeDeviceBoot(t, srv.URL, noBoot, token)
	if none.LastBootTime != nil || none.LastShutdownCause != nil || none.LastShutdownCauseCode != nil {
		t.Errorf("old-agent device should have null boot fields; got %+v", none)
	}
	if none.RecentReboots == nil {
		t.Error("recent_reboots should be an empty array, not null")
	}
	if len(none.RecentReboots) != 0 {
		t.Errorf("recent_reboots for old-agent device: got %d want 0", len(none.RecentReboots))
	}
}

type deviceBootResp struct {
	LastBootTime          *string `json:"last_boot_time"`
	LastShutdownCause     *string `json:"last_shutdown_cause"`
	LastShutdownCauseCode *int    `json:"last_shutdown_cause_code"`
	RecentReboots         []struct {
		BootTime          string  `json:"boot_time"`
		ShutdownCause     *string `json:"shutdown_cause"`
		ShutdownCauseCode *int    `json:"shutdown_cause_code"`
		DetectedAt        string  `json:"detected_at"`
	} `json:"recent_reboots"`
}

func decodeDeviceBoot(t *testing.T, baseURL, deviceID, token string) deviceBootResp {
	t.Helper()
	resp := doDeviceGet(t, baseURL, deviceID, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET device: status %d; body=%s", resp.StatusCode, raw)
	}
	var out deviceBootResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}
