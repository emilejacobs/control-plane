package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	commissionsvc "github.com/emilejacobs/control-plane/internal/cp/commission"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// Commissioner is the orchestration surface the handler drives.
type Commissioner interface {
	Commission(ctx context.Context, deviceID, correlationID string) (commissionsvc.Result, error)
}

// CommissionPostHandler serves POST /devices/{id}/commission — the staff-only
// "Commission" action (#91, ADR-036). It mints a per-device Tailscale key,
// gathers the ALPR license + PR token, and pushes cameras + the secrets to the
// device. Secrets never appear in the response, audit payload, or logs.
// Idempotent (the cmd carries the request correlation id); audited.
type CommissionPostHandler struct {
	svc   Commissioner
	audit audit.Writer
}

func NewCommissionPost(svc Commissioner, auditW audit.Writer) *CommissionPostHandler {
	return &CommissionPostHandler{svc: svc, audit: auditW}
}

func (h *CommissionPostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	deviceID := r.PathValue("id")
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed
	correlationID := cplog.CorrelationIDFromContext(r.Context())

	res, err := h.svc.Commission(r.Context(), deviceID, correlationID)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, registry.ErrDeviceNotFound):
			status = http.StatusNotFound
		case errors.Is(err, commissionsvc.ErrNotAssigned), errors.Is(err, commissionsvc.ErrNoPRToken):
			// Precondition not met — assign the device / set the PR token first.
			status = http.StatusConflict
		default:
			log.Error("audit.device_commission", "outcome", "error", "device_id", deviceID, "err", err)
		}
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action:       "audit.device_commission",
			ActorID:      claims.OperatorID,
			ActorType:    audit.ActorOperator,
			ResourceKind: "device",
			ResourceID:   deviceID,
			Outcome:      "error",
			SourceIP:     clientIP(r),
			UserAgent:    r.UserAgent(),
			Payload:      map[string]any{"reason": err.Error()},
		})
		http.Error(w, err.Error(), status)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:       "audit.device_commission",
		ActorID:      claims.OperatorID,
		ActorType:    audit.ActorOperator,
		ResourceKind: "device",
		ResourceID:   deviceID,
		Outcome:      "success",
		SourceIP:     clientIP(r),
		UserAgent:    r.UserAgent(),
		Payload:      map[string]any{"correlation_id": res.CorrelationID},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"correlation_id": res.CorrelationID})
}
