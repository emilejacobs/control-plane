package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// CameraStore is the read surface the cameras endpoint needs: the site-scoped
// fleet camera roll-up. Scoping is applied inside the store from the request's
// resolved SiteFilter, so the handler stays oblivious to authz.
type CameraStore interface {
	FleetCameras(ctx context.Context) (registry.FleetCameraRollup, error)
}

// CamerasHandler serves GET /fleet/cameras (#152) — the Overview's fleet camera
// roll-up: online/offline counts for the Cameras gauge plus the offline-camera
// list (label, device, site, since) for the Camera alerts panel.
type CamerasHandler struct {
	store CameraStore
}

func NewCameras(store CameraStore) *CamerasHandler {
	return &CamerasHandler{store: store}
}

type offlineCameraJSON struct {
	CameraID        string     `json:"camera_id"`
	Label           string     `json:"label"`
	DeviceID        string     `json:"device_id"`
	Hostname        string     `json:"hostname"`
	SiteName        *string    `json:"site_name"`
	StatusChangedAt *time.Time `json:"status_changed_at"`
}

type camerasResponse struct {
	Total   int                 `json:"total"`
	Online  int                 `json:"online"`
	Offline int                 `json:"offline"`
	Cameras []offlineCameraJSON `json:"cameras"`
}

func (h *CamerasHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rollup, err := h.store.FleetCameras(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := camerasResponse{
		Total:   rollup.Total,
		Online:  rollup.Online,
		Offline: len(rollup.Offline),
		Cameras: make([]offlineCameraJSON, 0, len(rollup.Offline)),
	}
	for _, c := range rollup.Offline {
		resp.Cameras = append(resp.Cameras, offlineCameraJSON{
			CameraID:        c.CameraID,
			Label:           c.Label,
			DeviceID:        c.DeviceID,
			Hostname:        c.Hostname,
			SiteName:        c.SiteName,
			StatusChangedAt: c.StatusChangedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
