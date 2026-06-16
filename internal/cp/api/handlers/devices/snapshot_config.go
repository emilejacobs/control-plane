package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	snapshotconfigproto "github.com/emilejacobs/control-plane/internal/protocol/snapshotconfig"
)

// SnapshotConfigStore is the persistence slice the snapshot-config PUT needs.
// *registry.Registry satisfies it; GetByID is site-scoped so an out-of-scope
// device 404s before any write.
type SnapshotConfigStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	SetSnapshotCadence(ctx context.Context, deviceID, cadence string) error
}

// === PUT /devices/{id}/snapshot-config ===

// SnapshotConfigHandler sets a device's scheduled-snapshot cadence (#9):
// persists it on the device row and pushes a snapshot.config cmd so the agent's
// scheduler picks it up. A nil publisher persists only (no push) — keeps tests
// without the downward channel simple.
type SnapshotConfigHandler struct {
	store     SnapshotConfigStore
	publisher CmdPublisher
	newCmdID  func() string
	now       func() time.Time
}

func NewSnapshotConfig(store SnapshotConfigStore, publisher CmdPublisher) *SnapshotConfigHandler {
	return &SnapshotConfigHandler{store: store, publisher: publisher, newCmdID: newRandomID, now: time.Now}
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

	// Push the new cadence to the agent so the scheduler picks it up. Best-
	// effort relative to the persisted row: an offline device gets it on the
	// next set (or a future reconcile). A publish error is surfaced so the
	// operator knows the agent didn't receive it.
	if h.publisher != nil {
		correlationID := cplog.CorrelationIDFromContext(r.Context())
		if correlationID == "" {
			correlationID = h.newCmdID()
		}
		args, _ := json.Marshal(snapshotconfigproto.Args{Cadence: body.Cadence})
		cmdBytes, _ := json.Marshal(envelope.Command{
			Type:          "snapshot.config",
			CorrelationID: correlationID,
			CommandID:     h.newCmdID(),
			Args:          args,
			IssuedAt:      h.now().UTC(),
		})
		if err := h.publisher.Publish(r.Context(), "devices/"+id+"/cmd", cmdBytes); err != nil {
			http.Error(w, "cadence saved but downstream publish failed: "+err.Error(), http.StatusBadGateway)
			return
		}
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
