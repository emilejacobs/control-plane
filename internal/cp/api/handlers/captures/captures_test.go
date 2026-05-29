package captures

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type fakeStore struct {
	list     []registry.Capture
	get      registry.Capture
	getErr   error
	lastKind string
}

func (f *fakeStore) ListCaptures(_ context.Context, _, kind string) ([]registry.Capture, error) {
	f.lastKind = kind
	return f.list, nil
}
func (f *fakeStore) GetCapture(context.Context, string) (registry.Capture, error) {
	return f.get, f.getErr
}

type fakePresigner struct{ url string }

func (f fakePresigner) GetURL(context.Context, string, time.Duration) (string, error) {
	return f.url, nil
}
func (f fakePresigner) PutURL(context.Context, string, string, time.Duration) (string, error) {
	return f.url, nil
}

func TestListHandler(t *testing.T) {
	store := &fakeStore{list: []registry.Capture{
		{ID: "c1", Kind: "snapshot", ContentType: "image/jpeg", SizeBytes: 100,
			Metadata: map[string]any{"camera_id": "cam1"}, CreatedAt: time.Unix(1700000000, 0).UTC()},
	}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/devices/dev-1/captures?kind=snapshot", nil)
	req.SetPathValue("id", "dev-1")
	NewList(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body)
	}
	if store.lastKind != "snapshot" {
		t.Errorf("kind filter = %q, want snapshot", store.lastKind)
	}
	var body struct {
		Captures []struct {
			ID          string         `json:"id"`
			Kind        string         `json:"kind"`
			ContentType string         `json:"content_type"`
			SizeBytes   int64          `json:"size_bytes"`
			Metadata    map[string]any `json:"metadata"`
			CreatedAt   string         `json:"created_at"`
		} `json:"captures"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Captures) != 1 || body.Captures[0].ID != "c1" || body.Captures[0].Kind != "snapshot" {
		t.Fatalf("captures = %+v", body.Captures)
	}
	if body.Captures[0].Metadata["camera_id"] != "cam1" {
		t.Errorf("metadata = %v", body.Captures[0].Metadata)
	}
	if body.Captures[0].CreatedAt != "2023-11-14T22:13:20Z" {
		t.Errorf("created_at = %q (want RFC3339 UTC)", body.Captures[0].CreatedAt)
	}
}

func TestURLHandlerSignsExistingCapture(t *testing.T) {
	store := &fakeStore{get: registry.Capture{ID: "c1", S3Key: "snapshots/x.jpg", ContentType: "image/jpeg"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/captures/c1/url", nil)
	req.SetPathValue("id", "c1")
	NewURL(store, fakePresigner{url: "https://s3.example/signed"}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.URL != "https://s3.example/signed" {
		t.Errorf("url = %q", body.URL)
	}
	if body.ContentType != "image/jpeg" || body.ExpiresIn != 300 {
		t.Errorf("content_type=%q expires_in=%d", body.ContentType, body.ExpiresIn)
	}
}

func TestURLHandlerNotFound(t *testing.T) {
	store := &fakeStore{getErr: registry.ErrCaptureNotFound}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/captures/missing/url", nil)
	req.SetPathValue("id", "missing")
	NewURL(store, fakePresigner{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
