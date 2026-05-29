package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

// TestCapturesAPI — GET /devices/{id}/captures lists through the real router;
// GET /captures/{id}/url returns a signed S3 download URL.
func TestCapturesAPI(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-captures-api", "ffffffff-0000-0000-0000-000000000002")
	cap, err := srv.Registry.InsertCapture(ctx, registry.CaptureInput{
		DeviceID: deviceID, Kind: "snapshot", S3Key: "snapshots/" + deviceID + "/cam1/1.jpg",
		ContentType: "image/jpeg", SizeBytes: 4242, Metadata: map[string]any{"camera_id": "cam1"},
	})
	if err != nil {
		t.Fatalf("InsertCapture: %v", err)
	}
	tok := mintAccessToken(t, ctx, srv)

	// List.
	resp := doJSON(t, http.MethodGet, srv.URL+"/devices/"+deviceID+"/captures?kind=snapshot", tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("list captures: %d; body=%s", resp.StatusCode, raw)
	}
	var list struct {
		Captures []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"captures"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Captures) != 1 || list.Captures[0].ID != cap.ID {
		t.Fatalf("captures = %+v, want the inserted snapshot", list.Captures)
	}

	// Signed URL.
	urlResp := doJSON(t, http.MethodGet, srv.URL+"/captures/"+cap.ID+"/url", tok, nil)
	defer urlResp.Body.Close()
	if urlResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(urlResp.Body)
		t.Fatalf("capture url: %d; body=%s", urlResp.StatusCode, raw)
	}
	var urlBody struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	_ = json.NewDecoder(urlResp.Body).Decode(&urlBody)
	if urlBody.ExpiresIn != 300 || urlBody.URL == "" {
		t.Errorf("url body = %+v, want a signed url + 300s expiry", urlBody)
	}
}
