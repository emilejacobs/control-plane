package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/authz"
)

// seedCamera inserts a camera on deviceID and, when status != "", drives it to
// that status at changedAt (UpdateCameraStatus advances status_changed_at on a
// change from the default "unknown"). Returns the camera id.
func seedCamera(t *testing.T, ctx context.Context, srv *testServer, deviceID, label, status string, changedAt time.Time) string {
	t.Helper()
	cam, err := srv.Registry.InsertCamera(ctx, deviceID, label, "rtsp://cam/"+label, false)
	if err != nil {
		t.Fatalf("insert camera %q: %v", label, err)
	}
	if status != "" {
		if err := srv.Registry.UpdateCameraStatus(ctx, deviceID, cam.CameraID, status, changedAt); err != nil {
			t.Fatalf("set camera %q status: %v", label, err)
		}
	}
	return cam.CameraID
}

// TestRegistryFleetCamerasRollup — the #152 fleet camera roll-up counts
// online/total across the fleet and returns the currently-offline cameras
// (with device + site context) ordered longest-outage-first. Unknown cameras
// count toward total but are neither online nor offline.
func TestRegistryFleetCamerasRollup(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	site := insertSite(t, ctx, srv, clientID, "Store 03")
	dev := insertDeviceAtSite(t, ctx, srv, "mac-cams", site)

	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	seedCamera(t, ctx, srv, dev, "Lot entrance", "offline", base)            // oldest outage → first
	seedCamera(t, ctx, srv, dev, "Drive-thru", "offline", base.Add(2*time.Hour))
	seedCamera(t, ctx, srv, dev, "Order point", "online", base)
	seedCamera(t, ctx, srv, dev, "Spare", "", base) // stays unknown

	rollup, err := srv.Registry.FleetCameras(staffCtx(ctx))
	if err != nil {
		t.Fatalf("FleetCameras: %v", err)
	}

	if rollup.Total != 4 || rollup.Online != 1 {
		t.Errorf("counts = total %d / online %d, want 4 / 1", rollup.Total, rollup.Online)
	}
	if len(rollup.Offline) != 2 {
		t.Fatalf("offline = %d, want 2", len(rollup.Offline))
	}
	if rollup.Offline[0].Label != "Lot entrance" || rollup.Offline[1].Label != "Drive-thru" {
		t.Errorf("offline order = [%s, %s], want [Lot entrance, Drive-thru] (longest-outage first)",
			rollup.Offline[0].Label, rollup.Offline[1].Label)
	}
	first := rollup.Offline[0]
	if first.Hostname != "mac-cams" || first.SiteName == nil || *first.SiteName != "Store 03" {
		t.Errorf("offline[0] context = host %q site %v, want mac-cams / Store 03", first.Hostname, first.SiteName)
	}
	if first.StatusChangedAt == nil || !first.StatusChangedAt.Equal(base) {
		t.Errorf("offline[0] status_changed_at = %v, want %v", first.StatusChangedAt, base)
	}
}

// TestRegistryFleetCamerasSiteScoped — a non-staff operator's roll-up only
// includes cameras at their allowlisted sites; an offline camera at a denied
// site never surfaces.
func TestRegistryFleetCamerasSiteScoped(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	siteAllowed := insertSite(t, ctx, srv, clientID, "Allowed Site")
	siteDenied := insertSite(t, ctx, srv, clientID, "Denied Site")
	devAllowed := insertDeviceAtSite(t, ctx, srv, "mac-allowed", siteAllowed)
	devDenied := insertDeviceAtSite(t, ctx, srv, "mac-denied", siteDenied)

	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	seedCamera(t, ctx, srv, devAllowed, "Allowed cam", "offline", base)
	seedCamera(t, ctx, srv, devDenied, "Denied cam", "offline", base)

	scoped := authz.ContextWithScope(ctx, authz.SiteFilter{SiteIDs: []string{siteAllowed}})
	rollup, err := srv.Registry.FleetCameras(scoped)
	if err != nil {
		t.Fatalf("FleetCameras: %v", err)
	}
	if rollup.Total != 1 {
		t.Errorf("total = %d, want 1 (denied-site camera leaked?)", rollup.Total)
	}
	if len(rollup.Offline) != 1 || rollup.Offline[0].Label != "Allowed cam" {
		t.Errorf("offline = %+v, want only [Allowed cam]", rollup.Offline)
	}
}

// TestRegistryFleetCamerasNoScopeFailsClosed — a read with no resolved scope
// returns an empty roll-up rather than the whole fleet.
func TestRegistryFleetCamerasNoScopeFailsClosed(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	site := insertSite(t, ctx, srv, clientID, "Store")
	dev := insertDeviceAtSite(t, ctx, srv, "mac-noscope-cam", site)
	seedCamera(t, ctx, srv, dev, "cam", "offline", time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC))

	rollup, err := srv.Registry.FleetCameras(ctx) // no ContextWithScope
	if err != nil {
		t.Fatalf("FleetCameras: %v", err)
	}
	if rollup.Total != 0 || len(rollup.Offline) != 0 {
		t.Errorf("unscoped read returned total %d / %d offline, want 0/0 (fail closed)", rollup.Total, len(rollup.Offline))
	}
}

// TestFleetCamerasEndpoint — GET /fleet/cameras round-trips the roll-up through
// the real router + auth + scope + DB under a {total, online, offline, cameras}
// envelope.
func TestFleetCamerasEndpoint(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "AcmeCorp")
	site := insertSite(t, ctx, srv, clientID, "Store 17")
	dev := insertDeviceAtSite(t, ctx, srv, "mac-cams-api", site)
	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	seedCamera(t, ctx, srv, dev, "Order point", "offline", base)
	seedCamera(t, ctx, srv, dev, "Lane", "online", base)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/fleet/cameras", nil)
	req.Header.Set("Authorization", "Bearer "+mintAccessToken(t, ctx, srv))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /fleet/cameras: status %d; body=%s", resp.StatusCode, raw)
	}

	var body struct {
		Total   int `json:"total"`
		Online  int `json:"online"`
		Offline int `json:"offline"`
		Cameras []struct {
			Label    string `json:"label"`
			Hostname string `json:"hostname"`
			SiteName string `json:"site_name"`
		} `json:"cameras"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 2 || body.Online != 1 || body.Offline != 1 {
		t.Errorf("counts = total %d / online %d / offline %d, want 2 / 1 / 1", body.Total, body.Online, body.Offline)
	}
	if len(body.Cameras) != 1 || body.Cameras[0].Label != "Order point" || body.Cameras[0].SiteName != "Store 17" {
		t.Errorf("cameras = %+v, want one Order point at Store 17", body.Cameras)
	}
}
