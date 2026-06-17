package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

// TestRegistryCamerasUpdate — UpdateCamera replaces label/rtsp_url/
// is_lpr for an existing row and returns the resulting state.
// Missing rows return ErrCameraNotFound. Setting is_lpr=true on a
// camera while another row already has it returns
// ErrCameraLPRConflict (same partial-unique semantics as Insert).
func TestRegistryCamerasUpdate(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cam-03", "33333333-2222-3333-4444-cccccccccccc")
	ctx = staffCtx(ctx)

	cam1, err := srv.Registry.InsertCamera(ctx, deviceID, "Old", "rtsp://old", false)
	if err != nil {
		t.Fatalf("InsertCamera: %v", err)
	}

	updated, err := srv.Registry.UpdateCamera(ctx, deviceID, cam1.CameraID, "New", "rtsp://new", true)
	if err != nil {
		t.Fatalf("UpdateCamera: %v", err)
	}
	if updated.CameraID != cam1.CameraID || updated.Label != "New" || updated.RtspURL != "rtsp://new" || !updated.IsLPR {
		t.Errorf("updated camera: got %+v", updated)
	}

	// Round-trip via ListCameras.
	list, err := srv.Registry.ListCameras(ctx, deviceID)
	if err != nil {
		t.Fatalf("ListCameras: %v", err)
	}
	if len(list) != 1 || list[0].Label != "New" || !list[0].IsLPR {
		t.Errorf("list after update: got %+v", list)
	}

	// Update on a missing camera_id returns ErrCameraNotFound.
	if _, err := srv.Registry.UpdateCamera(ctx, deviceID, "cam-missing", "x", "rtsp://x", false); !errors.Is(err, registry.ErrCameraNotFound) {
		t.Errorf("update missing: got %v want ErrCameraNotFound", err)
	}

	// Insert a second camera as non-LPR, then try to flip its LPR
	// flag while cam1 still has it ⇒ ErrCameraLPRConflict.
	cam2, err := srv.Registry.InsertCamera(ctx, deviceID, "Entry", "rtsp://e", false)
	if err != nil {
		t.Fatalf("InsertCamera #2: %v", err)
	}
	if _, err := srv.Registry.UpdateCamera(ctx, deviceID, cam2.CameraID, "Entry", "rtsp://e", true); !errors.Is(err, registry.ErrCameraLPRConflict) {
		t.Errorf("update flip second to LPR: got %v want ErrCameraLPRConflict", err)
	}
}

// TestRegistryCamerasDelete — DeleteCamera removes the row;
// ListCameras no longer returns it. Delete on a missing row returns
// ErrCameraNotFound (callers translate to 404). Delete frees up the
// is_lpr=true slot — a future insert can claim it.
func TestRegistryCamerasDelete(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cam-04", "44444444-2222-3333-4444-cccccccccccc")
	ctx = staffCtx(ctx)

	cam1, err := srv.Registry.InsertCamera(ctx, deviceID, "LPR-original", "rtsp://a", true)
	if err != nil {
		t.Fatalf("InsertCamera: %v", err)
	}

	if err := srv.Registry.DeleteCamera(ctx, deviceID, cam1.CameraID); err != nil {
		t.Fatalf("DeleteCamera: %v", err)
	}

	list, err := srv.Registry.ListCameras(ctx, deviceID)
	if err != nil {
		t.Fatalf("ListCameras: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("cameras after delete: got %d want 0", len(list))
	}

	// Delete a second time ⇒ ErrCameraNotFound.
	if err := srv.Registry.DeleteCamera(ctx, deviceID, cam1.CameraID); !errors.Is(err, registry.ErrCameraNotFound) {
		t.Errorf("delete missing: got %v want ErrCameraNotFound", err)
	}

	// The LPR slot is free again — a fresh insert with is_lpr=true
	// succeeds.
	if _, err := srv.Registry.InsertCamera(ctx, deviceID, "new-LPR", "rtsp://b", true); err != nil {
		t.Errorf("re-insert LPR after delete: got %v want success", err)
	}
}

// TestRegistryCameraStatus — #112 camera observability. A freshly
// inserted camera reads status "unknown" with null timestamps.
// UpdateCameraStatus sets status + last_checked_at and advances
// status_changed_at only on a real change: an idempotent re-report of
// the same status bumps last_checked_at but leaves status_changed_at
// fixed; a transition bumps both. An unknown camera_id returns
// ErrCameraNotFound.
func TestRegistryCameraStatus(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cam-05", "55555555-2222-3333-4444-cccccccccccc")
	ctx = staffCtx(ctx)

	cam, err := srv.Registry.InsertCamera(ctx, deviceID, "Drive-thru", "rtsp://a", false)
	if err != nil {
		t.Fatalf("InsertCamera: %v", err)
	}

	// Fresh camera: unknown status, null timestamps.
	list, err := srv.Registry.ListCamerasWithStatus(ctx, deviceID)
	if err != nil {
		t.Fatalf("ListCamerasWithStatus (fresh): %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("cameras: got %d want 1", len(list))
	}
	if list[0].Status != "unknown" || list[0].LastCheckedAt != nil || list[0].StatusChangedAt != nil {
		t.Errorf("fresh camera status: got status=%q checked=%v changed=%v want unknown/nil/nil",
			list[0].Status, list[0].LastCheckedAt, list[0].StatusChangedAt)
	}

	// First report: offline. status_changed_at == last_checked_at (the
	// unknown→offline transition).
	t1 := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	if err := srv.Registry.UpdateCameraStatus(ctx, deviceID, cam.CameraID, "offline", t1); err != nil {
		t.Fatalf("UpdateCameraStatus offline: %v", err)
	}
	list, _ = srv.Registry.ListCamerasWithStatus(ctx, deviceID)
	if list[0].Status != "offline" {
		t.Errorf("after first report: status got %q want offline", list[0].Status)
	}
	if list[0].LastCheckedAt == nil || !list[0].LastCheckedAt.Equal(t1) {
		t.Errorf("last_checked_at: got %v want %v", list[0].LastCheckedAt, t1)
	}
	if list[0].StatusChangedAt == nil || !list[0].StatusChangedAt.Equal(t1) {
		t.Errorf("status_changed_at: got %v want %v (transition)", list[0].StatusChangedAt, t1)
	}

	// Re-report same status at a later time: last_checked_at advances,
	// status_changed_at stays put.
	t2 := t1.Add(5 * time.Minute)
	if err := srv.Registry.UpdateCameraStatus(ctx, deviceID, cam.CameraID, "offline", t2); err != nil {
		t.Fatalf("UpdateCameraStatus offline (repeat): %v", err)
	}
	list, _ = srv.Registry.ListCamerasWithStatus(ctx, deviceID)
	if list[0].LastCheckedAt == nil || !list[0].LastCheckedAt.Equal(t2) {
		t.Errorf("last_checked_at after repeat: got %v want %v", list[0].LastCheckedAt, t2)
	}
	if list[0].StatusChangedAt == nil || !list[0].StatusChangedAt.Equal(t1) {
		t.Errorf("status_changed_at after repeat: got %v want %v (unchanged)", list[0].StatusChangedAt, t1)
	}

	// Transition back to online: both timestamps advance to t3.
	t3 := t2.Add(5 * time.Minute)
	if err := srv.Registry.UpdateCameraStatus(ctx, deviceID, cam.CameraID, "online", t3); err != nil {
		t.Fatalf("UpdateCameraStatus online: %v", err)
	}
	list, _ = srv.Registry.ListCamerasWithStatus(ctx, deviceID)
	if list[0].Status != "online" {
		t.Errorf("after recovery: status got %q want online", list[0].Status)
	}
	if list[0].StatusChangedAt == nil || !list[0].StatusChangedAt.Equal(t3) {
		t.Errorf("status_changed_at after recovery: got %v want %v", list[0].StatusChangedAt, t3)
	}

	// Unknown camera_id ⇒ ErrCameraNotFound.
	if err := srv.Registry.UpdateCameraStatus(ctx, deviceID, "cam-missing", "online", t3); !errors.Is(err, registry.ErrCameraNotFound) {
		t.Errorf("status update on missing camera: got %v want ErrCameraNotFound", err)
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
