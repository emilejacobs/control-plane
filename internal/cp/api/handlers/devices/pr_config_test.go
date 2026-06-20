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
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

// prStore fakes the persistence side of the PR-config handlers.
type prStore struct {
	known     map[string]bool
	cfg       map[string]prconfig.Config
	cfgExists map[string]bool
	cams      map[string][]cameras.Camera
	upserts   []prconfig.Config
	applied   []string
}

func (s *prStore) GetByID(_ context.Context, id string) (registry.Device, error) {
	if s.known[id] {
		return registry.Device{ID: id}, nil
	}
	return registry.Device{}, registry.ErrDeviceNotFound
}
func (s *prStore) GetPRConfig(_ context.Context, id string) (prconfig.Config, bool, error) {
	return s.cfg[id], s.cfgExists[id], nil
}
func (s *prStore) UpsertPRConfig(_ context.Context, id string, c prconfig.Config) (prconfig.Config, error) {
	s.upserts = append(s.upserts, c)
	if s.cfg == nil {
		s.cfg = map[string]prconfig.Config{}
	}
	s.cfg[id] = c
	return c, nil
}
func (s *prStore) ListCameras(_ context.Context, id string) ([]cameras.Camera, error) {
	return s.cams[id], nil
}
func (s *prStore) RecordPRConfigApplied(_ context.Context, id, _ string, _ time.Time) error {
	s.applied = append(s.applied, id)
	return nil
}

const prDev = "11111111-2222-3333-4444-555555555555"

func newPRStore() *prStore {
	return &prStore{
		known:     map[string]bool{prDev: true},
		cfg:       map[string]prconfig.Config{},
		cfgExists: map[string]bool{},
		cams: map[string][]cameras.Camera{prDev: {
			{CameraID: "cam1", Label: "Drive-thru", RtspURL: "rtsp://cam/lpr", IsLPR: true},
			{CameraID: "cam2", Label: "Entry", RtspURL: "rtsp://cam/entry", IsLPR: false},
		}},
	}
}

func doReq(t *testing.T, h http.Handler, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/devices/"+id+"/pr-config", rdr)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPRConfigGet(t *testing.T) {
	store := newPRStore()
	h := devices.NewPRConfigGet(store)

	// Fresh device: empty config + resolved LPR url, 200.
	rec := doReq(t, h, http.MethodGet, prDev, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET fresh: code %d", rec.Code)
	}
	var got struct {
		prconfig.Config
		LPRCameraRtspURL string `json:"lpr_camera_rtsp_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LPRCameraRtspURL != "rtsp://cam/lpr" {
		t.Errorf("lpr url = %q, want rtsp://cam/lpr", got.LPRCameraRtspURL)
	}

	// Unknown device: 404.
	if rec := doReq(t, h, http.MethodGet, "00000000-0000-0000-0000-000000000000", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET unknown: code %d, want 404", rec.Code)
	}
}

// capturePublisher records published topics for assertions.
type capturePublisher struct{ topics []string }

func (p *capturePublisher) Publish(_ context.Context, topic string, _ []byte) error {
	p.topics = append(p.topics, topic)
	return nil
}

func TestPRConfigPut(t *testing.T) {
	store := newPRStore()
	pub := &capturePublisher{}
	h := devices.NewPRConfigPut(store, pub)

	// Valid PUT: 200, upserted, response carries resolved LPR url.
	body := `{"camera_id":"0","region":"us-az","webhooks":[{"name":"prod","url":"https://api.uknomi.com/x","enabled":true,"image":true,"caching":false}]}`
	rec := doReq(t, h, http.MethodPut, prDev, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT valid: code %d body %s", rec.Code, rec.Body.String())
	}
	if len(store.upserts) != 1 || store.upserts[0].Region != "us-az" {
		t.Errorf("upsert not recorded correctly: %+v", store.upserts)
	}
	if len(pub.topics) != 1 || pub.topics[0] != "devices/"+prDev+"/cmd" {
		t.Errorf("expected one publish to the device cmd topic, got %v", pub.topics)
	}
	var got struct {
		LPRCameraRtspURL string `json:"lpr_camera_rtsp_url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.LPRCameraRtspURL != "rtsp://cam/lpr" {
		t.Errorf("response lpr url = %q", got.LPRCameraRtspURL)
	}

	// Invalid region: 400, store untouched.
	store.upserts = nil
	if rec := doReq(t, h, http.MethodPut, prDev, `{"camera_id":"0","region":"BAD REGION"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT invalid region: code %d, want 400", rec.Code)
	}
	if len(store.upserts) != 0 {
		t.Errorf("invalid PUT should not upsert: %+v", store.upserts)
	}

	// Unknown field rejected (strict whitelist).
	if rec := doReq(t, h, http.MethodPut, prDev, `{"camera_id":"0","region":"us-az","bogus":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT unknown field: code %d, want 400", rec.Code)
	}

	// Unknown device: 404.
	if rec := doReq(t, h, http.MethodPut, "00000000-0000-0000-0000-000000000000", body); rec.Code != http.StatusNotFound {
		t.Errorf("PUT unknown device: code %d, want 404", rec.Code)
	}
}

func TestPRConfigImport(t *testing.T) {
	store := newPRStore()
	h := devices.NewPRConfigImport(store)

	raw := "timezone = UTC\n[cameras]\n    regions = us-az\n    webhook_targets = prod\n    [[66_3]]\n        url = rtsp://cam/lpr\n        active = yes\n[webhooks]\n    [[prod]]\n        url = https://api.uknomi.com/x\n        name = prod\n        image = no\n        header = MAC: abc\n"
	rec := doReq(t, h, http.MethodPost, prDev, raw)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: code %d body %s", rec.Code, rec.Body.String())
	}
	// Extracted + upserted (no publish path on this handler).
	if len(store.upserts) != 1 {
		t.Fatalf("expected one upsert, got %d", len(store.upserts))
	}
	got := store.upserts[0]
	if got.Region != "us-az" || got.CameraID != "66_3" {
		t.Errorf("seeded config: %+v", got)
	}
	if len(got.Webhooks) != 1 || got.Webhooks[0].Name != "prod" || !got.Webhooks[0].Enabled {
		t.Errorf("seeded webhooks: %+v", got.Webhooks)
	}
	// Seeding must stamp last_applied (the device already runs this) so the
	// dashboard doesn't show a permanent "Pending".
	if len(store.applied) != 1 || store.applied[0] != prDev {
		t.Errorf("import should record applied: %v", store.applied)
	}

	// Unknown device: 404.
	if rec := doReq(t, h, http.MethodPost, "00000000-0000-0000-0000-000000000000", raw); rec.Code != http.StatusNotFound {
		t.Errorf("import unknown device: code %d, want 404", rec.Code)
	}
}
