package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// SnapshotConfigStore is the persistence slice the snapshot-config PUT needs.
// *registry.Registry satisfies it; GetByID is site-scoped so an out-of-scope
// device 404s before any write.
type SnapshotConfigStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	SetSnapshotCadence(ctx context.Context, deviceID, cadence string) error
}

// === PUT /devices/{id}/snapshot-config ===

// SnapshotConfigHandler sets a device's scheduled-snapshot cadence (#9). It
// persists the cadence on the device row; the agent learns it via config.update
// (wired in a later slice with the scheduler), so this slice is CP-side only.
type SnapshotConfigHandler struct {
	store SnapshotConfigStore
}

func NewSnapshotConfig(store SnapshotConfigStore) *SnapshotConfigHandler {
	return &SnapshotConfigHandler{store: store}
}

func (h *SnapshotConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Cadence string `json:"cadence"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeSnapshotConfigError(w, "snapshot_config.bad_payload", "invalid JSON body")
		return
	}
	if !registry.ValidSnapshotCadence(body.Cadence) {
		writeSnapshotConfigError(w, "snapshot_config.bad_cadence", "cadence must be one of off, daily, weekly")
		return
	}

	if err := h.store.SetSnapshotCadence(r.Context(), id, body.Cadence); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Cadence string `json:"cadence"`
	}{Cadence: body.Cadence})
}

func writeSnapshotConfigError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}
