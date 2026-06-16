package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// ALPRLicenseSetter is the registry surface the handler writes through.
type ALPRLicenseSetter interface {
	SetALPRLicense(ctx context.Context, deviceID, license string) error
}

// ALPRLicensePutHandler serves PUT /devices/{id}/alpr-license — the staff-only
// action that stores a device's Plate Recognizer license (#84, ADR-036 §5).
// The license is NOT pushed to the device here; Commission (#91) delivers it.
// The value is a secret: it is never logged and never appears in the audit
// payload (only license_set). Idempotent (PUT-registered) and audited.
type ALPRLicensePutHandler struct {
	svc   ALPRLicenseSetter
	audit audit.Writer
}

func NewALPRLicensePut(svc ALPRLicenseSetter, auditW audit.Writer) *ALPRLicensePutHandler {
	return &ALPRLicensePutHandler{svc: svc, audit: auditW}
}

type alprLicenseRequest struct {
	License string `json:"license"`
}

func (h *ALPRLicensePutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	deviceID := r.PathValue("id")
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed

	var req alprLicenseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.License == "" {
		http.Error(w, "license required", http.StatusBadRequest)
		return
	}

	if err := h.svc.SetALPRLicense(r.Context(), deviceID, req.License); err != nil {
		outcome := http.StatusInternalServerError
		if errors.Is(err, registry.ErrDeviceNotFound) {
			outcome = http.StatusNotFound
		} else {
			log.Error("audit.device_alpr_license", "outcome", "error", "device_id", deviceID, "err", err)
		}
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action:       "audit.device_alpr_license",
			ActorID:      claims.OperatorID,
			ActorType:    audit.ActorOperator,
			ResourceKind: "device",
			ResourceID:   deviceID,
			Outcome:      "error",
			SourceIP:     clientIP(r),
			UserAgent:    r.UserAgent(),
			// No license value in the payload — it is a secret.
			Payload: map[string]any{"license_set": false},
		})
		http.Error(w, http.StatusText(outcome), outcome)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:       "audit.device_alpr_license",
		ActorID:      claims.OperatorID,
		ActorType:    audit.ActorOperator,
		ResourceKind: "device",
		ResourceID:   deviceID,
		Outcome:      "success",
		SourceIP:     clientIP(r),
		UserAgent:    r.UserAgent(),
		Payload:      map[string]any{"license_set": true},
	})

	w.WriteHeader(http.StatusOK)
}
