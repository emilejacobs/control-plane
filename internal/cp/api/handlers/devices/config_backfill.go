package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	configbackfillproto "github.com/emilejacobs/control-plane/internal/protocol/configbackfill"
)

// backfillSnapshotStatePath is the install convention for the snapshot state
// file — it matches the value new devices receive from the install defaults
// (cmd/agent defaultAgentConfig). The backfill delivers it to existing devices
// whose config predates the field (#85).
const backfillSnapshotStatePath = "/var/uknomi/snapshot-state.json"

// ConfigBackfillStore is the persistence slice the handler needs — a site-scoped
// existence check so an out-of-scope/unknown device 404s before any publish.
type ConfigBackfillStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
}

// ConfigBackfillHandler serves POST /devices/{id}/config-backfill (#85) — the
// staff-only action that pushes install-time-only config fields
// (snapshot_state_path) to an already-enrolled device. Takes effect on the
// agent's next restart. Audited.
type ConfigBackfillHandler struct {
	store     ConfigBackfillStore
	publisher CmdPublisher
	audit     audit.Writer
	newCmdID  func() string
	now       func() time.Time
}

func NewConfigBackfill(store ConfigBackfillStore, publisher CmdPublisher, auditW audit.Writer) *ConfigBackfillHandler {
	return &ConfigBackfillHandler{store: store, publisher: publisher, audit: auditW, newCmdID: newRandomID, now: time.Now}
}

func (h *ConfigBackfillHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed

	if _, err := h.store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	correlationID := cplog.CorrelationIDFromContext(r.Context())
	if correlationID == "" {
		correlationID = h.newCmdID()
	}
	args, _ := json.Marshal(configbackfillproto.Args{SnapshotStatePath: backfillSnapshotStatePath})
	cmdBytes, _ := json.Marshal(envelope.Command{
		Type:          "config.backfill",
		CorrelationID: correlationID,
		CommandID:     h.newCmdID(),
		Args:          args,
		IssuedAt:      h.now().UTC(),
	})
	if err := h.publisher.Publish(r.Context(), "devices/"+id+"/cmd", cmdBytes); err != nil {
		http.Error(w, "downstream publish failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:       "audit.device_config_backfill",
		ActorID:      claims.OperatorID,
		ActorType:    audit.ActorOperator,
		ResourceKind: "device",
		ResourceID:   id,
		Outcome:      "success",
		SourceIP:     clientIP(r),
		UserAgent:    r.UserAgent(),
		Payload:      map[string]any{"snapshot_state_path": backfillSnapshotStatePath, "correlation_id": correlationID},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"correlation_id": correlationID})
}
