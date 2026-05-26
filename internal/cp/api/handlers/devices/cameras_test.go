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
// listing is the pre-populated cameras per device id for List tests.
type cameraStore struct {
	mu        sync.Mutex
	known     map[string]bool
	nextID    string
	inserts   []insertCall
	insertErr error
	listing   map[string][]cameras.Camera
	listErr   error
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

func (s *cameraStore) ListCameras(_ context.Context, deviceID string) ([]cameras.Camera, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listing[deviceID], nil
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

// GET /devices/{id}/cameras returns the cameras for that device under
// a `cameras` envelope (mirrors the {devices: [...]} list shape so
// dashboard parsers stay consistent). Empty list returns an empty
// array — not null — so the UI distinguishes "no cameras" from "no
// data".
func TestCameraListReturnsCameras(t *testing.T) {
	store := &cameraStore{
		known: map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{
			"dev-abc": {
				{CameraID: "cam1", Label: "Drive-thru", RtspURL: "rtsp://a", IsLPR: true},
				{CameraID: "cam2", Label: "Entry", RtspURL: "rtsp://b", IsLPR: false},
			},
		},
	}
	h := devices.NewCameraList(store)

	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/cameras", nil)
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Cameras []cameras.Camera `json:"cameras"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if len(got.Cameras) != 2 {
		t.Fatalf("cameras: got %d want 2", len(got.Cameras))
	}
	if got.Cameras[0].CameraID != "cam1" || got.Cameras[1].CameraID != "cam2" {
		t.Errorf("camera_ids: got %q,%q", got.Cameras[0].CameraID, got.Cameras[1].CameraID)
	}
	if !got.Cameras[0].IsLPR || got.Cameras[1].IsLPR {
		t.Errorf("is_lpr flags: got %v,%v want true,false", got.Cameras[0].IsLPR, got.Cameras[1].IsLPR)
	}
}

// Empty list returns an empty array, not null. The dashboard's
// rendering treats `cameras: []` as "no cameras yet" and `cameras:
// null` as "error" — so the API must not collapse one to the other.
func TestCameraListEmptyReturnsArrayNotNull(t *testing.T) {
	store := &cameraStore{
		known:   map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{},
	}
	h := devices.NewCameraList(store)

	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/cameras", nil)
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	// Check the raw body — JSON-decoding null vs [] both produce a
	// zero-length slice in Go, so structural inspection is required.
	got := strings.TrimSpace(rec.Body.String())
	if got != `{"cameras":[]}` {
		t.Errorf("body: got %q want %q", got, `{"cameras":[]}`)
	}
}

// Unknown fields are rejected with 400 — same protective stance as
// ADR-028's config.update whitelist. Prevents accidental drift if a
// future field is added on one side but not parsed correctly on the
// other; the API surfaces the typo immediately instead of silently
// discarding it.
func TestCameraPostRejectsUnknownFields(t *testing.T) {
	store := &cameraStore{known: map[string]bool{"dev-abc": true}, nextID: "cam1"}
	h := devices.NewCameraPost(store)

	body := `{"label":"x","rtsp_url":"rtsp://10.0.0.42/stream","is_lpr":false,"site_id":"site-1"}`
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

// rtsp_url must begin with rtsp:// or rtsps://. Catches the most
// common operator mistake (pasted the http: admin-UI URL by accident);
// permissive enough on everything after the scheme so vendor URLs with
// credentials + special chars (@, :, &) are not rejected.
func TestCameraPostRejectsBadRtspScheme(t *testing.T) {
	store := &cameraStore{known: map[string]bool{"dev-abc": true}, nextID: "cam1"}
	h := devices.NewCameraPost(store)

	cases := []string{
		`{"label":"x","rtsp_url":"http://10.0.0.42/stream","is_lpr":false}`,
		`{"label":"x","rtsp_url":"10.0.0.42/stream","is_lpr":false}`,
		`{"label":"x","rtsp_url":"","is_lpr":false}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/cameras", strings.NewReader(body))
		req.SetPathValue("id", "dev-abc")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status for body %s: got %d want 400; resp=%s", body, rec.Code, rec.Body.String())
		}
	}
	if len(store.inserts) != 0 {
		t.Errorf("InsertCamera must not be called on validation failure; got %d", len(store.inserts))
	}
}

// rtsps:// is the secure-RTSP scheme and must also be accepted.
func TestCameraPostAcceptsRtspsScheme(t *testing.T) {
	store := &cameraStore{known: map[string]bool{"dev-abc": true}, nextID: "cam1"}
	h := devices.NewCameraPost(store)

	body := `{"label":"x","rtsp_url":"rtsps://user:p@host:8322/stream","is_lpr":false}`
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/cameras", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("rtsps:// scheme should be accepted; got %d body=%s", rec.Code, rec.Body.String())
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
