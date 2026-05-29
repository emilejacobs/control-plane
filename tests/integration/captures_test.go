package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// TestRegistryCaptureStore — the #8 capture index round-trips: InsertCapture
// persists a row (server-assigned id + created_at, metadata jsonb);
// ListCaptures returns a device's captures newest-first, filterable by kind
// and site-scoped; GetCapture fetches one by id.
func TestRegistryCaptureStore(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-captures", "ffffffff-0000-0000-0000-000000000001")

	snap, err := srv.Registry.InsertCapture(ctx, registry.CaptureInput{
		DeviceID:    deviceID,
		Kind:        "snapshot",
		S3Key:       "snapshots/" + deviceID + "/cam1/1.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   12345,
		Metadata:    map[string]any{"camera_id": "cam1"},
	})
	if err != nil {
		t.Fatalf("InsertCapture(snapshot): %v", err)
	}
	if snap.ID == "" || snap.CreatedAt.IsZero() {
		t.Errorf("InsertCapture returned id=%q created_at=%v, want both set", snap.ID, snap.CreatedAt)
	}
	if snap.Metadata["camera_id"] != "cam1" {
		t.Errorf("metadata = %v, want camera_id=cam1", snap.Metadata)
	}

	if _, err := srv.Registry.InsertCapture(ctx, registry.CaptureInput{
		DeviceID: deviceID, Kind: "audio", S3Key: "audio/" + deviceID + "/1.wav",
		ContentType: "audio/wav", SizeBytes: 999,
	}); err != nil {
		t.Fatalf("InsertCapture(audio): %v", err)
	}

	// Site-scoped reads: a staff scope sees the device's captures.
	sctx := staffCtx(ctx)

	all, err := srv.Registry.ListCaptures(sctx, deviceID, "")
	if err != nil {
		t.Fatalf("ListCaptures(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListCaptures(all) = %d, want 2", len(all))
	}
	// Newest first: the audio capture was inserted second.
	if all[0].Kind != "audio" {
		t.Errorf("ListCaptures[0].Kind = %q, want audio (newest first)", all[0].Kind)
	}

	snaps, err := srv.Registry.ListCaptures(sctx, deviceID, "snapshot")
	if err != nil {
		t.Fatalf("ListCaptures(snapshot): %v", err)
	}
	if len(snaps) != 1 || snaps[0].Kind != "snapshot" {
		t.Errorf("ListCaptures(snapshot) = %+v, want one snapshot", snaps)
	}

	got, err := srv.Registry.GetCapture(sctx, snap.ID)
	if err != nil {
		t.Fatalf("GetCapture: %v", err)
	}
	if got.ID != snap.ID || got.S3Key != snap.S3Key {
		t.Errorf("GetCapture = %+v, want id/s3_key matching the snapshot", got)
	}

	if _, err := srv.Registry.GetCapture(sctx, "00000000-0000-0000-0000-0000000000ff"); !errors.Is(err, registry.ErrCaptureNotFound) {
		t.Errorf("GetCapture(unknown) err = %v, want ErrCaptureNotFound", err)
	}
}
