// Package captures serves the capture read surface (issue #8): the per-device
// capture list and the signed download URL. Both are site-scoped device reads
// (the store applies the operator's SiteFilter); the bytes live in S3 and the
// client fetches them directly via the signed URL.
package captures

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/captures"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// signedURLTTL is how long a download URL stays valid — short, since the
// dashboard mints one per render.
const signedURLTTL = 5 * time.Minute

// Store is the read surface the handlers need.
type Store interface {
	ListCaptures(ctx context.Context, deviceID, kind string) ([]registry.Capture, error)
	GetCapture(ctx context.Context, id string) (registry.Capture, error)
}

type captureJSON struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	ContentType string         `json:"content_type"`
	SizeBytes   int64          `json:"size_bytes"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   string         `json:"created_at"` // RFC3339
}

func toJSON(c registry.Capture) captureJSON {
	md := c.Metadata
	if md == nil {
		md = map[string]any{}
	}
	return captureJSON{
		ID: c.ID, Kind: c.Kind, ContentType: c.ContentType, SizeBytes: c.SizeBytes,
		Metadata: md, CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// ListHandler serves GET /devices/{id}/captures?kind=<kind> — newest-first,
// optionally filtered by kind.
type ListHandler struct{ store Store }

func NewList(store Store) *ListHandler { return &ListHandler{store} }

func (h *ListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	kind := r.URL.Query().Get("kind")
	list, err := h.store.ListCaptures(r.Context(), deviceID, kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]captureJSON, 0, len(list))
	for _, c := range list {
		items = append(items, toJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"captures": items})
}

// URLHandler serves GET /captures/{id}/url — a short-lived signed download
// URL for one capture (scoped: GetCapture won't resolve a capture outside the
// operator's site allowlist).
type URLHandler struct {
	store     Store
	presigner captures.Presigner
}

func NewURL(store Store, presigner captures.Presigner) *URLHandler {
	return &URLHandler{store, presigner}
}

func (h *URLHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := h.store.GetCapture(r.Context(), r.PathValue("id"))
	if errors.Is(err, registry.ErrCaptureNotFound) {
		http.Error(w, "capture not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	url, err := h.presigner.GetURL(r.Context(), c.S3Key, signedURLTTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":          url,
		"content_type": c.ContentType,
		"expires_in":   int(signedURLTTL.Seconds()),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
