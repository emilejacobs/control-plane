package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfigini"
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
// On success publishes a pr.config.update cmd carrying the saved config + the
// CP-resolved LPR camera url, so the agent merges it into config.ini and bounces
// the container.
type PRConfigPutHandler struct {
	store     PRConfigStore
	publisher CmdPublisher
	newCmdID  func() string
}

func NewPRConfigPut(store PRConfigStore, publisher CmdPublisher) *PRConfigPutHandler {
	return &PRConfigPutHandler{store: store, publisher: publisher, newCmdID: newRandomID}
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
	lprURL := resolveLPRURL(cams)

	// Push pr.config.update with the saved config + resolved LPR url. Persistence
	// already succeeded; a publish failure surfaces 502 so the operator retries
	// (dashboard pattern: save, then poll last_applied for "applied").
	if err := publishPRConfigUpdate(r.Context(), h.publisher, id, saved, lprURL, h.newCmdID); err != nil {
		http.Error(w, "publish pr.config.update: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, prConfigResponse{Config: saved, LPRCameraRtspURL: lprURL})
}

// publishPRConfigUpdate publishes a pr.config.update cmd on devices/{id}/cmd
// carrying the desired config + the CP-resolved LPR camera url.
func publishPRConfigUpdate(ctx context.Context, publisher CmdPublisher, deviceID string, cfg prconfig.Config, lprURL string, newCmdID func() string) error {
	args, err := json.Marshal(prconfig.UpdateRequest{Config: cfg, LPRCameraRtspURL: lprURL})
	if err != nil {
		return err
	}
	correlationID := cplog.CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = newCmdID()
	}
	cmd := envelope.Command{
		Type:          "pr.config.update",
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

// PRConfigImportHandler serves POST /devices/{id}/pr-config/import — seeds CP
// from a device's existing config.ini WITHOUT pushing back (the device already
// runs it). Body is the raw config.ini; the CP-managed subset is extracted and
// upserted. Used once during the Docker→Colima migration to seed CP from the
// captured configs before any dashboard edit, so the first real PUT doesn't
// clobber hand-tuned values.
type PRConfigImportHandler struct{ store PRConfigStore }

func NewPRConfigImport(store PRConfigStore) *PRConfigImportHandler {
	return &PRConfigImportHandler{store: store}
}

func (h *PRConfigImportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !deviceExists(w, r, h.store, id) {
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := prconfigini.Extract(raw)
	if err != nil {
		http.Error(w, "parse config.ini: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := prconfig.Validate(cfg); err != nil {
		http.Error(w, "extracted config invalid: "+err.Error(), http.StatusBadRequest)
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
	// No publish: seeding only populates CP to match what's already on the device.
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
