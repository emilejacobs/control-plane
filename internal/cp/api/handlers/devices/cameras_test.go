package devices_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// nopPublisher discards every Publish call — used for tests that
// don't exercise the downward channel. cameraCmdPublisher (declared
// further down) captures calls when assertions are needed.
type nopPublisher struct{}

func (nopPublisher) Publish(_ context.Context, _ string, _ []byte) error { return nil }

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
	updates       []updateCall
	deletes       []deleteCall
	camerasStatus registry.CamerasStatus
}

type insertCall struct {
	deviceID string
	label    string
	rtspURL  string
	isLPR    bool
}

type updateCall struct {
	deviceID string
	cameraID string
	label    string
	rtspURL  string
	isLPR    bool
}

type deleteCall struct {
	deviceID string
	cameraID string
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

func (s *cameraStore) UpdateCamera(_ context.Context, deviceID, cameraID, label, rtspURL string, isLPR bool) (cameras.Camera, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, updateCall{deviceID: deviceID, cameraID: cameraID, label: label, rtspURL: rtspURL, isLPR: isLPR})
	// Update the listing snapshot so a subsequent List in the same
	// test reflects the change (matches production registry semantics).
	for i, c := range s.listing[deviceID] {
		if c.CameraID == cameraID {
			s.listing[deviceID][i] = cameras.Camera{CameraID: cameraID, Label: label, RtspURL: rtspURL, IsLPR: isLPR}
			return s.listing[deviceID][i], nil
		}
	}
	return cameras.Camera{}, registry.ErrCameraNotFound
}

func (s *cameraStore) GetCamerasStatus(_ context.Context, deviceID string) (registry.CamerasStatus, error) {
	return s.camerasStatus, nil
}

func (s *cameraStore) DeleteCamera(_ context.Context, deviceID, cameraID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, deleteCall{deviceID: deviceID, cameraID: cameraID})
	for i, c := range s.listing[deviceID] {
		if c.CameraID == cameraID {
			s.listing[deviceID] = append(s.listing[deviceID][:i], s.listing[deviceID][i+1:]...)
			return nil
		}
	}
	return registry.ErrCameraNotFound
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
	h := devices.NewCameraPost(store, nopPublisher{})

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

// cameraCmdPublisher captures every Publish call so the test can
// inspect the cameras.update envelope and the post-CRUD list it
// carried.
type cameraCmdPublisher struct {
	mu     sync.Mutex
	calls  []cameraPubCall
	pubErr error
}

type cameraPubCall struct {
	topic   string
	payload []byte
}

func (p *cameraCmdPublisher) Publish(_ context.Context, topic string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, cameraPubCall{topic: topic, payload: append([]byte(nil), payload...)})
	return p.pubErr
}

// After a successful POST, a cameras.update cmd is published on
// devices/{id}/cmd carrying the full post-insert list. The agent's
// local cameras.json mirrors CP atomically (per ADR-030 § 1, payload
// is desired state, not deltas).
func TestCameraPostPublishesCamerasUpdate(t *testing.T) {
	store := &cameraStore{
		known:   map[string]bool{"dev-abc": true},
		nextID:  "cam1",
		listing: map[string][]cameras.Camera{"dev-abc": {}},
	}
	pub := &cameraCmdPublisher{}
	h := devices.NewCameraPost(store, pub)

	body := `{"label":"Drive-thru","rtsp_url":"rtsp://x","is_lpr":true}`
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/cameras", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()

	// Pre-populate listing so post-insert ListCameras returns the new row.
	store.listing["dev-abc"] = []cameras.Camera{{CameraID: "cam1", Label: "Drive-thru", RtspURL: "rtsp://x", IsLPR: true}}

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(pub.calls) != 1 {
		t.Fatalf("Publish calls: got %d want 1", len(pub.calls))
	}
	if pub.calls[0].topic != "devices/dev-abc/cmd" {
		t.Errorf("topic: got %q want devices/dev-abc/cmd", pub.calls[0].topic)
	}
	var cmd envelope.Command
	if err := json.Unmarshal(pub.calls[0].payload, &cmd); err != nil {
		t.Fatalf("payload not a valid Command: %v", err)
	}
	if cmd.Type != "cameras.update" {
		t.Errorf("cmd type: got %q want cameras.update", cmd.Type)
	}
	if cmd.CommandID == "" {
		t.Error("CommandID is empty; expected a fresh value")
	}
	var inner cameras.UpdateAllRequest
	if err := json.Unmarshal(cmd.Args, &inner); err != nil {
		t.Fatalf("args not valid UpdateAllRequest: %v", err)
	}
	if len(inner.Cameras) != 1 || inner.Cameras[0].CameraID != "cam1" {
		t.Errorf("payload cameras: got %+v want [{cam1...}]", inner.Cameras)
	}
}

// If Publish fails after a successful insert, the API returns 502.
// The store mutation still succeeded; the operator can retry, which
// will see the row already present and emit a fresh cmd.
func TestCameraPostPublishFailureReturns502(t *testing.T) {
	store := &cameraStore{
		known:   map[string]bool{"dev-abc": true},
		nextID:  "cam1",
		listing: map[string][]cameras.Camera{"dev-abc": {{CameraID: "cam1", Label: "x", RtspURL: "rtsp://x"}}},
	}
	pub := &cameraCmdPublisher{pubErr: errors.New("iot publish exploded")}
	h := devices.NewCameraPost(store, pub)

	body := `{"label":"x","rtsp_url":"rtsp://x","is_lpr":false}`
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/cameras", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d want 502; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.inserts) != 1 {
		t.Errorf("InsertCamera should still have been called (publish failure is downstream): got %d", len(store.inserts))
	}
}

// POST returns 409 Conflict when the store reports
// ErrCameraLPRConflict — the DB's partial unique index rejected a
// second is_lpr=true camera on the same device. Operator must
// un-flag the existing LPR camera first.
func TestCameraPostLPRConflictReturns409(t *testing.T) {
	store := &cameraStore{
		known:     map[string]bool{"dev-abc": true},
		nextID:    "cam2",
		insertErr: registry.ErrCameraLPRConflict,
	}
	h := devices.NewCameraPost(store, nopPublisher{})

	body := `{"label":"second-LPR","rtsp_url":"rtsp://x","is_lpr":true}`
	req := httptest.NewRequest(http.MethodPost, "/devices/dev-abc/cameras", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status: got %d want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// PUT returns 409 Conflict on the same condition.
func TestCameraPutLPRConflictReturns409(t *testing.T) {
	store := &cameraStore{
		known: map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{
			"dev-abc": {{CameraID: "cam2", Label: "x", RtspURL: "rtsp://x", IsLPR: false}},
		},
	}
	// The fake's UpdateCamera doesn't model the partial unique index;
	// inject the error via a wrapper.
	wrapped := &lprConflictPutStore{cameraStore: store}
	h := devices.NewCameraPut(wrapped, nopPublisher{})

	body := `{"label":"x","rtsp_url":"rtsp://x","is_lpr":true}`
	req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/cameras/cam2", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("camera_id", "cam2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status: got %d want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// lprConflictPutStore makes UpdateCamera always return
// ErrCameraLPRConflict; other methods delegate to the embedded fake.
type lprConflictPutStore struct {
	*cameraStore
}

func (s *lprConflictPutStore) UpdateCamera(_ context.Context, _, _, _, _ string, _ bool) (cameras.Camera, error) {
	return cameras.Camera{}, registry.ErrCameraLPRConflict
}

// All four handlers must return 404 when the store's GetByID
// reports ErrDeviceNotFound — which covers both "device doesn't
// exist" and "device exists but is filtered out of the operator's
// scope" (the registry conflates the two on purpose, fail-closed).
// Regression-pin against accidental removal of the early GetByID
// check that gates every handler.
func TestCameraHandlersReturn404WhenDeviceMissing(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		path    string
		body    string
		newH    func(devices.CameraStore) http.Handler
		setCam  bool
	}{
		{"POST", http.MethodPost, "/devices/dev-x/cameras", `{"label":"x","rtsp_url":"rtsp://x","is_lpr":false}`, func(s devices.CameraStore) http.Handler { return devices.NewCameraPost(s, nopPublisher{}) }, false},
		{"GET", http.MethodGet, "/devices/dev-x/cameras", "", func(s devices.CameraStore) http.Handler { return devices.NewCameraList(s) }, false},
		{"PUT", http.MethodPut, "/devices/dev-x/cameras/cam1", `{"label":"x","rtsp_url":"rtsp://x","is_lpr":false}`, func(s devices.CameraStore) http.Handler { return devices.NewCameraPut(s, nopPublisher{}) }, true},
		{"DELETE", http.MethodDelete, "/devices/dev-x/cameras/cam1", "", func(s devices.CameraStore) http.Handler { return devices.NewCameraDelete(s, nopPublisher{}) }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &cameraStore{} // empty known map ⇒ GetByID always ErrDeviceNotFound
			h := tc.newH(store)

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.SetPathValue("id", "dev-x")
			if tc.setCam {
				req.SetPathValue("camera_id", "cam1")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status: got %d want 404; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// DELETE /devices/{id}/cameras/{camera_id} removes the camera and
// returns 204 No Content. Returns 404 if the row doesn't exist.
func TestCameraDeleteRemovesCamera(t *testing.T) {
	store := &cameraStore{
		known: map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{
			"dev-abc": {{CameraID: "cam1", Label: "x", RtspURL: "rtsp://x", IsLPR: false}},
		},
	}
	h := devices.NewCameraDelete(store, nopPublisher{})

	req := httptest.NewRequest(http.MethodDelete, "/devices/dev-abc/cameras/cam1", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("camera_id", "cam1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.deletes) != 1 || store.deletes[0].cameraID != "cam1" {
		t.Errorf("DeleteCamera calls: got %+v", store.deletes)
	}
}

func TestCameraDeleteMissingReturns404(t *testing.T) {
	store := &cameraStore{
		known:   map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{"dev-abc": {}},
	}
	h := devices.NewCameraDelete(store, nopPublisher{})

	req := httptest.NewRequest(http.MethodDelete, "/devices/dev-abc/cameras/cam-missing", nil)
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("camera_id", "cam-missing")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rec.Code)
	}
}

// PUT /devices/{id}/cameras/{camera_id} replaces the camera's
// mutable fields (label, rtsp_url, is_lpr) and returns the updated
// row. Same validation as POST.
func TestCameraPutUpdatesCamera(t *testing.T) {
	store := &cameraStore{
		known: map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{
			"dev-abc": {{CameraID: "cam1", Label: "Old", RtspURL: "rtsp://old", IsLPR: false}},
		},
	}
	h := devices.NewCameraPut(store, nopPublisher{})

	body := `{"label":"New","rtsp_url":"rtsp://new","is_lpr":true}`
	req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/cameras/cam1", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("camera_id", "cam1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got cameras.Camera
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if got.CameraID != "cam1" || got.Label != "New" || got.RtspURL != "rtsp://new" || !got.IsLPR {
		t.Errorf("updated camera: got %+v", got)
	}
	if len(store.updates) != 1 {
		t.Fatalf("UpdateCamera calls: got %d want 1", len(store.updates))
	}
	if store.updates[0].cameraID != "cam1" {
		t.Errorf("cameraID: got %q", store.updates[0].cameraID)
	}
}

// PUT on a camera_id that doesn't exist returns 404.
func TestCameraPutMissingCameraReturns404(t *testing.T) {
	store := &cameraStore{
		known:   map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{"dev-abc": {}},
	}
	h := devices.NewCameraPut(store, nopPublisher{})

	body := `{"label":"x","rtsp_url":"rtsp://x","is_lpr":false}`
	req := httptest.NewRequest(http.MethodPut, "/devices/dev-abc/cameras/cam-missing", strings.NewReader(body))
	req.SetPathValue("id", "dev-abc")
	req.SetPathValue("camera_id", "cam-missing")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404; body=%s", rec.Code, rec.Body.String())
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
	// The body MUST contain "cameras":[] explicitly (not null).
	got := strings.TrimSpace(rec.Body.String())
	if !strings.Contains(got, `"cameras":[]`) {
		t.Errorf("body should contain `\"cameras\":[]` (explicit empty array), got %q", got)
	}
}

// GET surfaces the cameras_last_applied_at + corr_id mirror columns
// so the dashboard can render a "pending vs applied" badge. Null
// before the agent has ACKed; populated afterward.
func TestCameraListSurfacesLastAppliedAt(t *testing.T) {
	at := time.Date(2026, 5, 26, 12, 30, 0, 0, time.UTC)
	corr := "corr-applied-xyz"
	store := &cameraStore{
		known:   map[string]bool{"dev-abc": true},
		listing: map[string][]cameras.Camera{"dev-abc": {}},
		camerasStatus: registry.CamerasStatus{
			LastAppliedAt:            &at,
			LastAppliedCorrelationID: &corr,
		},
	}
	h := devices.NewCameraList(store)

	req := httptest.NewRequest(http.MethodGet, "/devices/dev-abc/cameras", nil)
	req.SetPathValue("id", "dev-abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	var got struct {
		LastAppliedAt            *string `json:"last_applied_at"`
		LastAppliedCorrelationID *string `json:"last_applied_correlation_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LastAppliedAt == nil || *got.LastAppliedAt != "2026-05-26T12:30:00Z" {
		t.Errorf("last_applied_at: got %v want RFC3339 of %v", got.LastAppliedAt, at)
	}
	if got.LastAppliedCorrelationID == nil || *got.LastAppliedCorrelationID != corr {
		t.Errorf("last_applied_correlation_id: got %v", got.LastAppliedCorrelationID)
	}
}

// Unknown fields are rejected with 400 — same protective stance as
// ADR-028's config.update whitelist. Prevents accidental drift if a
// future field is added on one side but not parsed correctly on the
// other; the API surfaces the typo immediately instead of silently
// discarding it.
func TestCameraPostRejectsUnknownFields(t *testing.T) {
	store := &cameraStore{known: map[string]bool{"dev-abc": true}, nextID: "cam1"}
	h := devices.NewCameraPost(store, nopPublisher{})

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
	h := devices.NewCameraPost(store, nopPublisher{})

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
	h := devices.NewCameraPost(store, nopPublisher{})

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
	h := devices.NewCameraPost(store, nopPublisher{})

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
