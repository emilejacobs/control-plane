// Package enrollment serves POST /enrollments — the install-script-driven
// device enrollment endpoint defined in PRD § API contracts.
package enrollment

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type Service interface {
	Enroll(ctx context.Context, in registry.EnrollInput) (registry.EnrollOutput, error)
}

type Handler struct {
	svc Service
}

func New(svc Service) *Handler { return &Handler{svc: svc} }

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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
