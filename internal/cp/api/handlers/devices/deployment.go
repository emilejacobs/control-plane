package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// DeploymentSetter is the registry surface the handler writes through.
type DeploymentSetter interface {
	SetDeployment(ctx context.Context, deviceID string, siteID, assetNumber *string) error
}

// DeploymentPutHandler serves PUT /devices/{id}/deployment — the
// staff-only "Edit deployment" action from the dashboard's device
// page. Updates site_id + asset_number in a single write. Idempotent
// (the route is PUT-registered, so the middleware enforces
// Idempotency-Key) and audited under audit.device_deployment.
type DeploymentPutHandler struct {
	svc   DeploymentSetter
	audit audit.Writer
}

func NewDeploymentPut(svc DeploymentSetter, auditW audit.Writer) *DeploymentPutHandler {
	return &DeploymentPutHandler{svc: svc, audit: auditW}
}

// deploymentRequest accepts explicit nulls for both fields. PUT
// semantics: missing fields decode as zero (nil), which clears the
// column. Dashboard pre-populates the modal with current values and
// always sends both — clients that want to update just one re-send
// the prior value of the other.
type deploymentRequest struct {
	SiteID      *string `json:"site_id"`
	AssetNumber *string `json:"asset_number"`
}

func (h *DeploymentPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	deviceID := r.PathValue("id")
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed

	var req deploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if err := h.svc.SetDeployment(r.Context(), deviceID, req.SiteID, req.AssetNumber); err != nil {
		switch {
		case errors.Is(err, registry.ErrDeviceNotFound):
			http.Error(w, "device not found", http.StatusNotFound)
		case errors.Is(err, registry.ErrSiteNotFound):
			http.Error(w, "site not found", http.StatusBadRequest)
		default:
			log.Error("audit.device_deployment", "outcome", "error", "device_id", deviceID, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action:       "audit.device_deployment",
			ActorID:      claims.OperatorID,
			ActorType:    audit.ActorOperator,
			ResourceKind: "device",
			ResourceID:   deviceID,
			Outcome:      "error",
			SourceIP:     clientIP(r),
			UserAgent:    r.UserAgent(),
			Payload: map[string]any{
				"site_id":      req.SiteID,
				"asset_number": req.AssetNumber,
				"err":          err.Error(),
			},
		})
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:       "audit.device_deployment",
		ActorID:      claims.OperatorID,
		ActorType:    audit.ActorOperator,
		ResourceKind: "device",
		ResourceID:   deviceID,
		Outcome:      "success",
		SourceIP:     clientIP(r),
		UserAgent:    r.UserAgent(),
		Payload: map[string]any{
			"site_id":      req.SiteID,
			"asset_number": req.AssetNumber,
		},
	})

	w.WriteHeader(http.StatusOK)
}

// clientIP strips the port from r.RemoteAddr — same helper shape as
// the other devices/* handlers.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
