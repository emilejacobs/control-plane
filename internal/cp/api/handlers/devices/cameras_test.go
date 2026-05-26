package devices_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// cameraStore stubs the persistence side of the cameras handlers.
// known maps device IDs that exist + are visible to the caller's
// scope (the handler treats both not-found and not-visible as 404).
// nextID is the server-assigned id the next Insert returns.
type cameraStore struct {
	mu        sync.Mutex
	known     map[string]bool
	nextID    string
	inserts   []insertCall
	insertErr error
}

type insertCall struct {
	deviceID string
	label    string
	rtspURL  string
	isLPR    bool
}

func (s *cameraStore) GetByID(_ context.Context, id string) (registry.Device, error) {
	if s.known[id] {
		return registry.Device{ID: id}, nil
	}
	return registry.Device{}, registry.ErrDeviceNotFound
}

func (s *cameraStore) InsertCamera(_ context.Context, deviceID, label, rtspURL string, isLPR bool) (cameras.Camera, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insertErr != nil {
		return cameras.Camera{}, s.insertErr
	}
	s.inserts = append(s.inserts, insertCall{deviceID: deviceID, label: label, rtspURL: rtspURL, isLPR: isLPR})
	return cameras.Camera{
		CameraID: s.nextID,
		Label:    label,
		RtspURL:  rtspURL,
		IsLPR:    isLPR,
	}, nil
}

// Tracer bullet: POST /devices/{id}/cameras with a valid body returns
// 201 and the newly-created camera in JSON, including the server-
// assigned camera_id. This exercises the routing → body parse → store
// hop → JSON response path end-to-end.
func TestCameraPostReturns201WithNewCamera(t *testing.T) {
	store := &cameraStore{
		known:  map[string]bool{"dev-abc": true},
		nextID: "cam1",
	}
	h := devices.NewCameraPost(store)

	body := `{"label":"Drive-thru","rtsp_url":"rtsp://user:pass@10.0.0.42/stream","is_lpr":true}`
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/cameras", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201; body=%s", rec.Code, rec.Body.String())
	}

	var got cameras.Camera
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if got.CameraID != "cam1" {
		t.Errorf("camera_id: got %q want cam1 (server-assigned)", got.CameraID)
	}
	if got.Label != "Drive-thru" {
		t.Errorf("label: got %q want Drive-thru", got.Label)
	}
	if got.RtspURL != "rtsp://user:pass@10.0.0.42/stream" {
		t.Errorf("rtsp_url: got %q", got.RtspURL)
	}
	if !got.IsLPR {
		t.Error("is_lpr: got false, want true")
	}

	if len(store.inserts) != 1 {
		t.Fatalf("InsertCamera calls: got %d want 1", len(store.inserts))
	}
	c := store.inserts[0]
	if c.deviceID != "dev-abc" || c.label != "Drive-thru" || c.rtspURL != "rtsp://user:pass@10.0.0.42/stream" || !c.isLPR {
		t.Errorf("InsertCamera args: got %+v", c)
	}
}

// Empty / whitespace-only labels are rejected with 400; InsertCamera
// is never called. A camera with no label is unidentifiable to the
// operator — same hygiene as not allowing empty hostnames.
func TestCameraPostRejectsEmptyLabel(t *testing.T) {
	store := &cameraStore{known: map[string]bool{"dev-abc": true}, nextID: "cam1"}
	h := devices.NewCameraPost(store)

	body := `{"label":"   ","rtsp_url":"rtsp://10.0.0.42/stream","is_lpr":false}`
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/cameras", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.inserts) != 0 {
		t.Errorf("InsertCamera must not be called on validation failure; got %d", len(store.inserts))
	}
}
