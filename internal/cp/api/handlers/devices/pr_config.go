package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

// PRConfigStore is the persistence side of the Plate Recognizer config handlers
// (issue #5). *registry.Registry satisfies it; tests use a fake.
type PRConfigStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	GetPRConfig(ctx context.Context, deviceID string) (prconfig.Config, bool, error)
	UpsertPRConfig(ctx context.Context, deviceID string, c prconfig.Config) (prconfig.Config, error)
	ListCameras(ctx context.Context, deviceID string) ([]cameras.Camera, error)
}

// prConfigResponse is the GET/PUT body: the CP-managed config plus the
// read-only LPR camera RTSP URL, resolved from the cameras inventory (the
// is_lpr=true row) so the dashboard can show it without it being editable here.
type prConfigResponse struct {
	prconfig.Config
	LPRCameraRtspURL string `json:"lpr_camera_rtsp_url"`
}

// prConfigPutRequest is the editable subset accepted on PUT — deliberately
// excludes the last_applied_* audit fields (stamped on the agent apply-ack).
// image/caching are per-webhook (on prconfig.Webhook), not global.
type prConfigPutRequest struct {
	CameraID string             `json:"camera_id"`
	Region   string             `json:"region"`
	Webhooks []prconfig.Webhook `json:"webhooks"`
}

func resolveLPRURL(cams []cameras.Camera) string {
	for _, c := range cams {
		if c.IsLPR {
			return c.RtspURL
		}
	}
	return ""
}

// PRConfigGetHandler serves GET /devices/{id}/pr-config.
type PRConfigGetHandler struct{ store PRConfigStore }

func NewPRConfigGet(store PRConfigStore) *PRConfigGetHandler {
	return &PRConfigGetHandler{store: store}
}

func (h *PRConfigGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !deviceExists(w, r, h.store, id) {
		return
	}

	cfg, exists, err := h.store.GetPRConfig(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		// No row yet: empty config (region/camera_id blank, no webhooks). The
		// dashboard pre-populates the form from the captured/seeded values.
		cfg = prconfig.Config{}
	}

	cams, err := h.store.ListCameras(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, prConfigResponse{Config: cfg, LPRCameraRtspURL: resolveLPRURL(cams)})
}

// PRConfigPutHandler serves PUT /devices/{id}/pr-config — validates + upserts
// the editable subset. Persist-only; the pr.config.update push to the agent is
// wired in a later slice once the agent handler exists.
type PRConfigPutHandler struct{ store PRConfigStore }

func NewPRConfigPut(store PRConfigStore) *PRConfigPutHandler {
	return &PRConfigPutHandler{store: store}
}

func (h *PRConfigPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !deviceExists(w, r, h.store, id) {
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req prConfigPutRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := prconfig.Config{
		CameraID: req.CameraID,
		Region:   req.Region,
		Webhooks: req.Webhooks,
	}
	if err := prconfig.Validate(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	saved, err := h.store.UpsertPRConfig(r.Context(), id, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cams, err := h.store.ListCameras(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, prConfigResponse{Config: saved, LPRCameraRtspURL: resolveLPRURL(cams)})
}

// deviceExists writes a 404 (not found / not in scope) or 500 and returns false
// when the device isn't visible to the caller; true when it is.
func deviceExists(w http.ResponseWriter, r *http.Request, store interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
}, id string) bool {
	if _, err := store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return false
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
