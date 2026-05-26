package devices

import (
	"bytes"
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
	ListCameras(ctx context.Context, deviceID string) ([]cameras.Camera, error)
	UpdateCamera(ctx context.Context, deviceID, cameraID, label, rtspURL string, isLPR bool) (cameras.Camera, error)
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
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		http.Error(w, "label is required", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.RtspURL, "rtsp://") && !strings.HasPrefix(req.RtspURL, "rtsps://") {
		http.Error(w, "rtsp_url must begin with rtsp:// or rtsps://", http.StatusBadRequest)
		return
	}

	cam, err := h.store.InsertCamera(r.Context(), id, label, req.RtspURL, req.IsLPR)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cam)
}

// CameraListHandler serves GET /devices/{id}/cameras — returns the
// cameras inventory for one device under a {cameras: [...]} envelope.
type CameraListHandler struct {
	store CameraStore
}

func NewCameraList(store CameraStore) *CameraListHandler {
	return &CameraListHandler{store: store}
}

type cameraListResponse struct {
	Cameras []cameras.Camera `json:"cameras"`
}

func (h *CameraListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if _, err := h.store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	list, err := h.store.ListCameras(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Empty array, not null — dashboard distinguishes "no cameras"
	// from "error".
	if list == nil {
		list = []cameras.Camera{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cameraListResponse{Cameras: list})
}

// CameraPutHandler serves PUT /devices/{id}/cameras/{camera_id} —
// replaces the camera's mutable fields. Same validation as POST.
// Returns 404 if the (device_id, camera_id) row doesn't exist.
type CameraPutHandler struct {
	store CameraStore
}

func NewCameraPut(store CameraStore) *CameraPutHandler {
	return &CameraPutHandler{store: store}
}

func (h *CameraPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cameraID := r.PathValue("camera_id")

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
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		http.Error(w, "label is required", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.RtspURL, "rtsp://") && !strings.HasPrefix(req.RtspURL, "rtsps://") {
		http.Error(w, "rtsp_url must begin with rtsp:// or rtsps://", http.StatusBadRequest)
		return
	}

	cam, err := h.store.UpdateCamera(r.Context(), id, cameraID, label, req.RtspURL, req.IsLPR)
	if err != nil {
		if errors.Is(err, registry.ErrCameraNotFound) {
			http.Error(w, "camera not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cam)
}
