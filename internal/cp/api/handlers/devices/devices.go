// Package devices serves the read-side device endpoints defined in
// PRD § API contracts (GET /devices/{id}, GET /devices).
package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type Service interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	List(ctx context.Context) ([]registry.Device, error)
}

type GetHandler struct {
	svc Service
	// now is the clock used to compute mtls_cert_days_remaining at response
	// time. Defaults to time.Now; tests override it for a deterministic now.
	now func() time.Time
}

func NewGet(svc Service) *GetHandler { return &GetHandler{svc: svc, now: time.Now} }

type response struct {
	DeviceID              string  `json:"device_id"`
	Hostname              string  `json:"hostname"`
	HardwareUUID          string  `json:"hardware_uuid"`
	HardwareKind          string  `json:"hardware_kind"`
	OSVersion             string  `json:"os_version"`
	AgentVersion          string  `json:"agent_version"`
	IoTThingARN           string  `json:"iot_thing_arn"`
	IsOnline              bool    `json:"is_online"`
	LastSeenAgoSeconds    *int64  `json:"last_seen_ago_seconds"`
	MtlsCertExpiresAt     *string `json:"mtls_cert_expires_at"`
	MtlsCertDaysRemaining *int    `json:"mtls_cert_days_remaining"`
	EnrolledAt            string  `json:"enrolled_at"`
}

// ListHandler serves GET /devices — the site-scoped fleet list. It runs
// behind the scope middleware; registry.List filters by the operator's
// SiteFilter.
type ListHandler struct {
	svc Service
}

func NewList(svc Service) *ListHandler { return &ListHandler{svc: svc} }

type listItem struct {
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname"`
	IsOnline bool   `json:"is_online"`
	// SiteName / ClientName are null for a device with no site assigned;
	// the fleet view groups those under "Unassigned".
	SiteName   *string `json:"site_name"`
	ClientName *string `json:"client_name"`
}

type listResponse struct {
	Devices []listItem `json:"devices"`
}

func (h *ListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	devs, err := h.svc.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]listItem, 0, len(devs))
	for _, d := range devs {
		items = append(items, listItem{
			DeviceID:   d.ID,
			Hostname:   d.Hostname,
			IsOnline:   d.IsOnline,
			SiteName:   d.SiteName,
			ClientName: d.ClientName,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(listResponse{Devices: items})
}

func (h *GetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// is_online is the stored presence state maintained by cp-ingest;
	// last_seen_ago_seconds is derived from the raw last_seen column and is
	// null for a device that has never reported a heartbeat.
	var agoSeconds *int64
	if dev.LastSeen != nil {
		s := int64(time.Since(*dev.LastSeen).Seconds())
		agoSeconds = &s
	}

	// mtls_cert_expires_at is the cert notAfter persisted at enrollment;
	// mtls_cert_days_remaining is the whole days from now until then,
	// computed at response time. Both are null only for rows that predate
	// migration 006.
	var certExpiresAt *string
	var certDaysRemaining *int
	if dev.MtlsCertExpiresAt != nil {
		s := dev.MtlsCertExpiresAt.UTC().Format(time.RFC3339)
		certExpiresAt = &s
		d := int(dev.MtlsCertExpiresAt.Sub(h.now()).Hours() / 24)
		certDaysRemaining = &d
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{
		DeviceID:              dev.ID,
		Hostname:              dev.Hostname,
		HardwareUUID:          dev.HardwareUUID,
		HardwareKind:          dev.HardwareKind,
		OSVersion:             dev.OSVersion,
		AgentVersion:          dev.AgentVersion,
		IoTThingARN:           dev.IoTThingARN,
		IsOnline:              dev.IsOnline,
		LastSeenAgoSeconds:    agoSeconds,
		MtlsCertExpiresAt:     certExpiresAt,
		MtlsCertDaysRemaining: certDaysRemaining,
		EnrolledAt:            dev.EnrolledAt.UTC().Format(time.RFC3339),
	})
}
