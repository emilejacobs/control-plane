// Package enrollment serves POST /enrollments — the install-script-driven
// device enrollment endpoint defined in PRD § API contracts.
package enrollment

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// hostnameConvention is the project device-naming pattern (ADR-017). A
// hostname that does not match still enrolls — the regex is a sanity check
// that raises an audit alert, not an allowlist that blocks.
var hostnameConvention = regexp.MustCompile(`^(mac-mini|pi|radxa)-[a-z0-9-]+-\d{2}$`)

// sourceIP is the client address an enrollment request arrived from, without
// the port — the audit log keys anomaly detection on it (ADR-017).
func sourceIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type Service interface {
	Enroll(ctx context.Context, in registry.EnrollInput) (registry.EnrollOutput, error)
}

type Handler struct {
	svc   Service
	audit audit.Writer
}

// New returns an enrollment handler. auditW may be nil; api.NewBuilderWith
// substitutes audit.SlogOnly so unit tests that omit the writer still emit
// the audit.* slog lines their assertions grep on.
func New(svc Service, auditW audit.Writer) *Handler {
	if auditW == nil {
		auditW = audit.SlogOnly{}
	}
	return &Handler{svc: svc, audit: auditW}
}

type request struct {
	BootstrapKey string `json:"bootstrap_key"`
	Hostname     string `json:"hostname"`
	HardwareUUID string `json:"hardware_uuid"`
	HardwareKind string `json:"hardware_kind"`
	OSVersion    string `json:"os_version"`
	AgentVersion string `json:"agent_version"`
}

type response struct {
	DeviceID          string `json:"device_id"`
	MtlsCertPEM       string `json:"mtls_cert_pem"`
	MtlsPrivateKeyPEM string `json:"mtls_private_key_pem"`
	IoTEndpoint       string `json:"iot_endpoint"`
	IoTThingARN       string `json:"iot_thing_arn"`
	MtlsCertExpiresAt string `json:"mtls_cert_expires_at"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	out, err := h.svc.Enroll(r.Context(), registry.EnrollInput{
		BootstrapKey: req.BootstrapKey,
		Hostname:     req.Hostname,
		HardwareUUID: req.HardwareUUID,
		HardwareKind: req.HardwareKind,
		OSVersion:    req.OSVersion,
		AgentVersion: req.AgentVersion,
	})
	if err != nil {
		if errors.Is(err, registry.ErrInvalidBootstrapKey) {
			_ = h.audit.Write(r.Context(), audit.Entry{
				Action:    "audit.enrollment",
				ActorType: audit.ActorAgent,
				Outcome:   "failure",
				SourceIP:  sourceIP(r),
				UserAgent: r.UserAgent(),
				Payload: map[string]any{
					"reason":        "invalid_bootstrap_key",
					"hardware_uuid": req.HardwareUUID,
					"hostname":      req.Hostname,
				},
			})
			http.Error(w, "invalid bootstrap key", http.StatusUnauthorized)
			return
		}
		log.Error("audit.enrollment",
			"outcome", "error",
			"source_ip", sourceIP(r),
			"hardware_uuid", req.HardwareUUID,
			"hostname", req.Hostname,
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:       "audit.enrollment",
		ActorID:      out.DeviceID,
		ActorType:    audit.ActorAgent,
		ResourceKind: "device",
		ResourceID:   out.DeviceID,
		Outcome:      "success",
		SourceIP:     sourceIP(r),
		UserAgent:    r.UserAgent(),
		Payload: map[string]any{
			"hardware_uuid": req.HardwareUUID,
			"hostname":      req.Hostname,
			"device_id":     out.DeviceID,
		},
	})
	if !hostnameConvention.MatchString(req.Hostname) {
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action:       "audit.enrollment.anomaly",
			ActorID:      out.DeviceID,
			ActorType:    audit.ActorAgent,
			ResourceKind: "device",
			ResourceID:   out.DeviceID,
			Outcome:      "alert",
			SourceIP:     sourceIP(r),
			UserAgent:    r.UserAgent(),
			Payload: map[string]any{
				"alert":         "hostname_convention",
				"hardware_uuid": req.HardwareUUID,
				"hostname":      req.Hostname,
				"device_id":     out.DeviceID,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response{
		DeviceID:          out.DeviceID,
		MtlsCertPEM:       out.MtlsCertPEM,
		MtlsPrivateKeyPEM: out.MtlsPrivateKeyPEM,
		IoTThingARN:       out.IoTThingARN,
		MtlsCertExpiresAt: out.MtlsCertExpiresAt.UTC().Format(time.RFC3339),
	})
}
