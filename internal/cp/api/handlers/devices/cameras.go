package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// CameraStore is the persistence side of the cameras handlers. Narrow
// interface so the handler tests run against a fake; *registry.Registry
// satisfies it in production.
type CameraStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	InsertCamera(ctx context.Context, deviceID, label, rtspURL string, isLPR bool) (cameras.Camera, error)
}

// CameraPostHandler serves POST /devices/{id}/cameras — the create
// endpoint for a new camera under a device. Camera IDs are server-
// assigned (cam1, cam2, ... per device) by the store.
type CameraPostHandler struct {
	store CameraStore
}

func NewCameraPost(store CameraStore) *CameraPostHandler {
	return &CameraPostHandler{store: store}
}

type cameraCreateRequest struct {
	Label   string `json:"label"`
	RtspURL string `json:"rtsp_url"`
	IsLPR   bool   `json:"is_lpr"`
}

func (h *CameraPostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if _, err := h.store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req cameraCreateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cam, err := h.store.InsertCamera(r.Context(), id, strings.TrimSpace(req.Label), req.RtspURL, req.IsLPR)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cam)
}
