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
	// ListServices returns the per-service rows for one device, ordered
	// by service_name (the dashboard renders them in that order). Returns
	// an empty slice (not nil) for a device that has never reported.
	ListServices(ctx context.Context, deviceID string) ([]registry.DeviceService, error)
	// GetServiceConfig returns the per-device override + last-applied
	// tracking (Phase 2 slice 2). Zero-valued for a device with no
	// override ever set; non-nil pointers indicate present values.
	GetServiceConfig(ctx context.Context, deviceID string) (registry.ServiceConfig, error)
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
	// SiteName / ClientName are null for a device with no site assigned;
	// the per-device view shows "Unassigned" for those.
	SiteName   *string `json:"site_name"`
	ClientName *string `json:"client_name"`
	// AssetNumber is the fleet-tracking identifier (migration 014).
	// Null until install-module 11 populates it; rendered as
	// "Unassigned" on the per-device Deployment card.
	AssetNumber *string `json:"asset_number"`
	// LanIP, TailscaleIP, TailscaleName are the three network
	// fields the agent publishes on every heartbeat (issue #14;
	// migration 018). Null until the first heartbeat-post-rollout
	// lands. The dashboard's edgePreviewURL prefers TailscaleName
	// over Hostname (the bench-Mac drift case), and CamerasPanel
	// renders a "Copy LAN URL" affordance when LanIP is non-null.
	LanIP         *string `json:"lan_ip"`
	TailscaleIP   *string `json:"tailscale_ip"`
	TailscaleName *string `json:"tailscale_name"`
	// Services is the per-service state snapshot from the agent's last
	// service-status report (Phase 2). Empty array (not null) for a
	// device that has never reported — the dashboard distinguishes
	// "no report yet" from a missing field.
	Services []serviceItem `json:"services"`

	// ServiceConfig surfaces the Phase 2 slice 2 per-device override +
	// last-applied tracking. Always present (never null) — its
	// internal fields may be null. Distinguishes "default" from
	// "overridden" via allow_list_override != null on the dashboard.
	ServiceConfig serviceConfigItem `json:"service_config"`
}

type serviceConfigItem struct {
	AllowListOverride        *[]string `json:"allow_list_override"`
	IntervalOverride         *string   `json:"interval_override"`
	LastAppliedAt            *string   `json:"last_applied_at"`
	LastAppliedCorrelationID *string   `json:"last_applied_correlation_id"`
}

type serviceItem struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	StateSince   string `json:"state_since"`   // RFC3339
	LastReported string `json:"last_reported"` // RFC3339
}

// ListHandler serves GET /devices — the site-scoped fleet list. It runs
// behind the scope middleware; registry.List filters by the operator's
// SiteFilter.
type ListHandler struct {
	svc Service
	// now is the clock used to compute mtls_cert_days_remaining at
	// response time. Defaults to time.Now; tests override.
	now func() time.Time
}

func NewList(svc Service) *ListHandler { return &ListHandler{svc: svc, now: time.Now} }

type listItem struct {
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname"`
	IsOnline bool   `json:"is_online"`
	// SiteName / ClientName are null for a device with no site assigned;
	// the fleet view groups those under "Unassigned".
	SiteName   *string `json:"site_name"`
	ClientName *string `json:"client_name"`
	// Phase 2 Chain A: surface the cert + agent_version fields the
	// overview tiles aggregate over (Cert expiring ≤ 30d, Agent
	// version drift, Cert expiring soonest). The data already exists
	// on registry.Device; the LIST endpoint had been dropping it.
	AgentVersion          string  `json:"agent_version"`
	MtlsCertExpiresAt     *string `json:"mtls_cert_expires_at"`     // RFC3339; null for rows that predate migration 006
	MtlsCertDaysRemaining *int    `json:"mtls_cert_days_remaining"` // computed; null when cert_expires_at is null
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
		var certExpiresAt *string
		var certDaysRemaining *int
		if d.MtlsCertExpiresAt != nil {
			s := d.MtlsCertExpiresAt.UTC().Format(time.RFC3339)
			certExpiresAt = &s
			days := int(d.MtlsCertExpiresAt.Sub(h.now()).Hours() / 24)
			certDaysRemaining = &days
		}
		items = append(items, listItem{
			DeviceID:              d.ID,
			Hostname:              d.Hostname,
			IsOnline:              d.IsOnline,
			SiteName:              d.SiteName,
			ClientName:            d.ClientName,
			AgentVersion:          d.AgentVersion,
			MtlsCertExpiresAt:     certExpiresAt,
			MtlsCertDaysRemaining: certDaysRemaining,
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

	// Services for the per-device "Services" panel. A failure to read
	// the panel data should not 500 the whole device-view fetch —
	// presence + cert + identity all stay useful. Log the error and
	// surface an empty array; the dashboard then renders "no report yet".
	rows, err := h.svc.ListServices(r.Context(), dev.ID)
	if err != nil {
		rows = nil
	}
	services := make([]serviceItem, 0, len(rows))
	for _, row := range rows {
		services = append(services, serviceItem{
			Name:         row.Name,
			State:        string(row.State),
			StateSince:   row.StateSince.UTC().Format(time.RFC3339),
			LastReported: row.LastReported.UTC().Format(time.RFC3339),
		})
	}

	// Phase 2 slice 2: override + last-applied tracking. Same
	// resilience posture as services — a read failure here returns
	// the zero-value config (all fields nil) so the rest of the page
	// still renders.
	cfg, _ := h.svc.GetServiceConfig(r.Context(), dev.ID)
	var lastAppliedAt *string
	if cfg.LastAppliedAt != nil {
		s := cfg.LastAppliedAt.UTC().Format(time.RFC3339)
		lastAppliedAt = &s
	}
	serviceConfig := serviceConfigItem{
		AllowListOverride:        cfg.AllowListOverride,
		IntervalOverride:         cfg.IntervalOverride,
		LastAppliedAt:            lastAppliedAt,
		LastAppliedCorrelationID: cfg.LastAppliedCorrelationID,
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
		SiteName:              dev.SiteName,
		ClientName:            dev.ClientName,
		AssetNumber:           dev.AssetNumber,
		LanIP:                 dev.LanIP,
		TailscaleIP:           dev.TailscaleIP,
		TailscaleName:         dev.TailscaleName,
		Services:              services,
		ServiceConfig:         serviceConfig,
	})
}
