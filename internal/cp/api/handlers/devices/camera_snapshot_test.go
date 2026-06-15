package devices_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	snapshotproto "github.com/emilejacobs/control-plane/internal/protocol/camerasnapshot"
)

type snapshotStore struct{ known map[string]bool }

func (s *snapshotStore) GetByID(_ context.Context, id string) (registry.Device, error) {
	if s.known[id] {
		return registry.Device{ID: id}, nil
	}
	return registry.Device{}, registry.ErrDeviceNotFound
}

type fakeCapturePresigner struct {
	putKey, putContentType string
	putExpiry              time.Duration
}

func (f *fakeCapturePresigner) GetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://s3.example/get/" + key, nil
}
func (f *fakeCapturePresigner) PutURL(_ context.Context, key, contentType string, expiry time.Duration) (string, error) {
	f.putKey, f.putContentType, f.putExpiry = key, contentType, expiry
	return "https://s3.example/put/" + key, nil
}

// POST /devices/{id}/snapshot presigns a PUT, publishes camera.snapshot on the
// device cmd topic carrying {camera_id, s3_key, put_url}, and returns 202.
func TestCameraSnapshotPostHappyPath(t *testing.T) {
	store := &snapshotStore{known: map[string]bool{"dev-abc": true}}
	pre := &fakeCapturePresigner{}
	pub := &cmdPublisher{}
	h := devices.NewCameraSnapshot(store, pre, pub)

	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/snapshot",
		strings.NewReader(`{"camera_id":"cam1"}`))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	// Presigned a PUT for a snapshots/ key as image/jpeg with a 5-min TTL.
	if !strings.HasPrefix(pre.putKey, "snapshots/dev-abc/") || !strings.HasSuffix(pre.putKey, ".jpg") {
		t.Errorf("presigned key = %q", pre.putKey)
	}
	if pre.putContentType != snapshotproto.ContentType || pre.putExpiry != 5*time.Minute {
		t.Errorf("presign ct/ttl = %q/%v", pre.putContentType, pre.putExpiry)
	}
	if len(pub.calls) != 1 || pub.calls[0].topic != "devices/dev-abc/cmd" {
		t.Fatalf("publish calls = %+v", pub.calls)
	}
	var cmd envelope.Command
	if err := json.Unmarshal(pub.calls[0].payload, &cmd); err != nil {
		t.Fatalf("cmd: %v", err)
	}
	if cmd.Type != "camera.snapshot" {
		t.Errorf("cmd type = %q", cmd.Type)
	}
	var args snapshotproto.Args
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		t.Fatalf("args: %v", err)
	}
	if args.CameraID != "cam1" || args.S3Key != pre.putKey || args.PutURL == "" {
		t.Errorf("args = %+v (want camera cam1, key %q)", args, pre.putKey)
	}
}

func TestCameraSnapshotPostUnknownDevice(t *testing.T) {
	h := devices.NewCameraSnapshot(&snapshotStore{known: map[string]bool{}}, &fakeCapturePresigner{}, &cmdPublisher{})
	req := httptest.NewRequest(http.MethodPost, "/devices/missing/snapshot", strings.NewReader(`{"camera_id":"cam1"}`))
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCameraSnapshotPostRequiresCameraID(t *testing.T) {
	store := &snapshotStore{known: map[string]bool{"dev-abc": true}}
	pub := &cmdPublisher{}
	h := devices.NewCameraSnapshot(store, &fakeCapturePresigner{}, pub)
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/snapshot", strings.NewReader(`{}`))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(pub.calls) != 0 {
		t.Errorf("should not publish on bad request: %+v", pub.calls)
	}
}
