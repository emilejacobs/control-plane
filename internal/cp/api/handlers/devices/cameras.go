package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
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
	DeleteCamera(ctx context.Context, deviceID, cameraID string) error
	GetCamerasStatus(ctx context.Context, deviceID string) (registry.CamerasStatus, error)
}

// publishCamerasUpdate fetches the post-mutation list from the store
// and publishes a cameras.update command on devices/{id}/cmd. The
// payload always carries the full current list so the agent sees
// desired state, not deltas — same pattern as slice 2's
// config.update. Correlation_id is propagated from the request
// context if middleware set one; otherwise a fresh value is minted.
func publishCamerasUpdate(ctx context.Context, store CameraStore, publisher CmdPublisher, deviceID string, newCmdID func() string) error {
	list, err := store.ListCameras(ctx, deviceID)
	if err != nil {
		return err
	}
	if list == nil {
		list = []cameras.Camera{}
	}
	args, err := json.Marshal(cameras.UpdateAllRequest{Cameras: list})
	if err != nil {
		return err
	}
	correlationID := cplog.CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = newCmdID()
	}
	cmd := envelope.Command{
		Type:          "cameras.update",
		CorrelationID: correlationID,
		CommandID:     newCmdID(),
		Args:          args,
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return publisher.Publish(ctx, "devices/"+deviceID+"/cmd", payload)
}

// CameraPostHandler serves POST /devices/{id}/cameras — the create
// endpoint for a new camera under a device. Camera IDs are server-
// assigned (cam1, cam2, ... per device) by the store. On success
// publishes a cameras.update cmd carrying the full post-insert list
// so the agent's local cameras.json mirrors CP atomically.
type CameraPostHandler struct {
	store     CameraStore
	publisher CmdPublisher
	newCmdID  func() string
}

func NewCameraPost(store CameraStore, publisher CmdPublisher) *CameraPostHandler {
	return &CameraPostHandler{store: store, publisher: publisher, newCmdID: newRandomID}
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
	if err := cameras.ValidateCamera(req.Label, req.RtspURL); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label := strings.TrimSpace(req.Label)

	cam, err := h.store.InsertCamera(r.Context(), id, label, req.RtspURL, req.IsLPR)
	if err != nil {
		if errors.Is(err, registry.ErrCameraLPRConflict) {
			http.Error(w, "another camera on this device already has is_lpr=true; unflag it first", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Publish cameras.update with the post-insert list. Persistence
	// already succeeded; on a publish failure we surface 502 so the
	// operator can retry (the registry insert is idempotent on the
	// same camera_id only by virtue of the unique constraint — but
	// the dashboard pattern is "save, then poll for applied".
	if err := publishCamerasUpdate(r.Context(), h.store, h.publisher, id, h.newCmdID); err != nil {
		http.Error(w, "publish cameras.update: "+err.Error(), http.StatusBadGateway)
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
	// LastAppliedAt is the timestamp of the most recent cameras.update
	// ACK from the device. Null until the agent has ACKed at least
	// once. Dashboard derives a "pending vs applied" badge from this
	// + the most recent device_cameras.updated_at.
	LastAppliedAt            *string `json:"last_applied_at"`
	LastAppliedCorrelationID *string `json:"last_applied_correlation_id"`
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
	status, err := h.store.GetCamerasStatus(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var appliedAt *string
	if status.LastAppliedAt != nil {
		s := status.LastAppliedAt.UTC().Format(time.RFC3339)
		appliedAt = &s
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cameraListResponse{
		Cameras:                  list,
		LastAppliedAt:            appliedAt,
		LastAppliedCorrelationID: status.LastAppliedCorrelationID,
	})
}

// CameraPutHandler serves PUT /devices/{id}/cameras/{camera_id} —
// replaces the camera's mutable fields. Same validation as POST.
// Returns 404 if the (device_id, camera_id) row doesn't exist. On
// success publishes a cameras.update cmd carrying the full
// post-update list.
type CameraPutHandler struct {
	store     CameraStore
	publisher CmdPublisher
	newCmdID  func() string
}

func NewCameraPut(store CameraStore, publisher CmdPublisher) *CameraPutHandler {
	return &CameraPutHandler{store: store, publisher: publisher, newCmdID: newRandomID}
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
	if err := cameras.ValidateCamera(req.Label, req.RtspURL); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label := strings.TrimSpace(req.Label)

	cam, err := h.store.UpdateCamera(r.Context(), id, cameraID, label, req.RtspURL, req.IsLPR)
	if err != nil {
		if errors.Is(err, registry.ErrCameraNotFound) {
			http.Error(w, "camera not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, registry.ErrCameraLPRConflict) {
			http.Error(w, "another camera on this device already has is_lpr=true; unflag it first", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := publishCamerasUpdate(r.Context(), h.store, h.publisher, id, h.newCmdID); err != nil {
		http.Error(w, "publish cameras.update: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cam)
}

// CameraDeleteHandler serves DELETE /devices/{id}/cameras/{camera_id}.
// Returns 204 No Content on success, 404 if the row doesn't exist.
// On success publishes a cameras.update cmd carrying the full
// post-delete list.
type CameraDeleteHandler struct {
	store     CameraStore
	publisher CmdPublisher
	newCmdID  func() string
}

func NewCameraDelete(store CameraStore, publisher CmdPublisher) *CameraDeleteHandler {
	return &CameraDeleteHandler{store: store, publisher: publisher, newCmdID: newRandomID}
}

func (h *CameraDeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	if err := h.store.DeleteCamera(r.Context(), id, cameraID); err != nil {
		if errors.Is(err, registry.ErrCameraNotFound) {
			http.Error(w, "camera not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := publishCamerasUpdate(r.Context(), h.store, h.publisher, id, h.newCmdID); err != nil {
		http.Error(w, "publish cameras.update: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
