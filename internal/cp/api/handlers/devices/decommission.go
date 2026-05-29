package devices

import (
	"context"
	"errors"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// DeviceDeleter is the decommission surface: remove a device row.
type DeviceDeleter interface {
	DeleteDevice(ctx context.Context, id string) error
}

// DeleteHandler serves DELETE /devices/{id} — staff-only device
// decommission (the CP-side row removal; AWS IoT thing/cert teardown is
// out-of-band per the decommission runbook). Audited; 204 on success, 404
// when the device is unknown or already gone.
type DeleteHandler struct {
	svc   DeviceDeleter
	audit audit.Writer
}

func NewDelete(svc DeviceDeleter, auditW audit.Writer) *DeleteHandler {
	return &DeleteHandler{svc: svc, audit: auditW}
}

func (h *DeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claims, _ := middleware.OperatorFromContext(r.Context())

	err := h.svc.DeleteDevice(r.Context(), id)
	if errors.Is(err, registry.ErrDeviceNotFound) {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	if err != nil {
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action: "audit.device_decommission", ActorID: claims.OperatorID, ActorType: audit.ActorOperator,
			ResourceKind: "device", ResourceID: id, Outcome: "error",
			SourceIP: clientIP(r), UserAgent: r.UserAgent(),
			Payload: map[string]any{"err": err.Error()},
		})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.audit.Write(r.Context(), audit.Entry{
		Action: "audit.device_decommission", ActorID: claims.OperatorID, ActorType: audit.ActorOperator,
		ResourceKind: "device", ResourceID: id, Outcome: "success",
		SourceIP: clientIP(r), UserAgent: r.UserAgent(),
	})
	w.WriteHeader(http.StatusNoContent)
}
