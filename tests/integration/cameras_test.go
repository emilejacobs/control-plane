package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// TestRegistryCamerasInsertList — Phase 2 cameras inventory slice
// (issue #2). InsertCamera assigns a server-side camera_id of the
// form camN per device, starting at cam1; ListCameras returns the
// rows for that device in insertion order. The round-trip preserves
// label, rtsp_url, and is_lpr verbatim.
func TestRegistryCamerasInsertList(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cam-01", "11111111-2222-3333-4444-cccccccccccc")
	ctx = staffCtx(ctx)

	// Fresh device: empty cameras list.
	list, err := srv.Registry.ListCameras(ctx, deviceID)
	if err != nil {
		t.Fatalf("ListCameras (fresh): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("fresh device cameras: got %d want 0", len(list))
	}

	// Insert first camera. Server-assigned cam1.
	cam1, err := srv.Registry.InsertCamera(ctx, deviceID, "Drive-thru", "rtsp://a/stream", true)
	if err != nil {
		t.Fatalf("InsertCamera #1: %v", err)
	}
	if cam1.CameraID != "cam1" {
		t.Errorf("first camera id: got %q want cam1", cam1.CameraID)
	}
	if cam1.Label != "Drive-thru" || cam1.RtspURL != "rtsp://a/stream" || !cam1.IsLPR {
		t.Errorf("first camera fields: got %+v", cam1)
	}

	// Insert second camera. Server-assigned cam2.
	cam2, err := srv.Registry.InsertCamera(ctx, deviceID, "Entry", "rtsp://b/stream", false)
	if err != nil {
		t.Fatalf("InsertCamera #2: %v", err)
	}
	if cam2.CameraID != "cam2" {
		t.Errorf("second camera id: got %q want cam2", cam2.CameraID)
	}

	// List returns both.
	list, err = srv.Registry.ListCameras(ctx, deviceID)
	if err != nil {
		t.Fatalf("ListCameras (after inserts): %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("cameras after 2 inserts: got %d want 2", len(list))
	}
	if list[0].CameraID != "cam1" || list[1].CameraID != "cam2" {
		t.Errorf("camera order: got %q,%q want cam1,cam2", list[0].CameraID, list[1].CameraID)
	}
}

// TestRegistryCamerasLPRConflict — DB partial unique index enforces
// at-most-one is_lpr=true per device. The second insert with
// is_lpr=true on the same device returns registry.ErrCameraLPRConflict
// so the API can translate to 409.
func TestRegistryCamerasLPRConflict(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cam-02", "22222222-2222-3333-4444-cccccccccccc")
	ctx = staffCtx(ctx)

	if _, err := srv.Registry.InsertCamera(ctx, deviceID, "first-LPR", "rtsp://a", true); err != nil {
		t.Fatalf("InsertCamera #1: %v", err)
	}
	_, err := srv.Registry.InsertCamera(ctx, deviceID, "second-LPR", "rtsp://b", true)
	if !errors.Is(err, registry.ErrCameraLPRConflict) {
		t.Errorf("second LPR insert: got %v want ErrCameraLPRConflict", err)
	}

	// Non-LPR insert succeeds afterward — the conflict is specifically
	// about a second is_lpr=true row, not about further additions.
	if _, err := srv.Registry.InsertCamera(ctx, deviceID, "non-LPR", "rtsp://c", false); err != nil {
		t.Errorf("non-LPR insert after LPR-conflict: got %v want success", err)
	}
}
