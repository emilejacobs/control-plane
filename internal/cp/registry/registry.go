// Package registry owns the enrollment-first device lifecycle.
//
// Per PRD § Module decomposition: Enroll wraps bootstrap-key validation,
// IoT Core thing+cert minting, and the Postgres insert behind one interface,
// so callers never see AWS or DB primitives.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

// ErrInvalidBootstrapKey is returned by Enroll when the supplied bootstrap
// key is rejected by the verifier. Handlers translate it to HTTP 401 (per
// PRD § API contracts).
var ErrInvalidBootstrapKey = errors.New("invalid bootstrap key")

// ErrSiteNotFound is returned by SetDeployment when the supplied
// site_id doesn't exist in the local mirror. The staff-only handler
// translates it to 400 rather than letting an FK violation surface
// as a 500.
var ErrSiteNotFound = errors.New("site not found")

// ErrDeviceNotFound is returned by GetByID when no row matches the id.
// Handlers translate it to HTTP 404.
var ErrDeviceNotFound = errors.New("device not found")

// ErrLogTailNotFound is returned by GetLogTail when no row matches the
// correlation_id. Handlers translate it to HTTP 404.
var ErrLogTailNotFound = errors.New("log tail not found")

// ErrCameraNotFound is returned when a cameras-table operation cannot
// locate the (device_id, camera_id) pair. Used by UpdateCamera and
// DeleteCamera to distinguish "no such row" from a real DB error.
var ErrCameraNotFound = errors.New("camera not found")

// ErrNetworkScanNotFound is returned by GetNetworkScan when no row
// matches the correlation_id. Handlers translate it to HTTP 404.
var ErrNetworkScanNotFound = errors.New("network scan not found")

// ErrCaptureNotFound is returned by GetCapture when no capture row matches
// the id (or it's outside the operator's scope). Handlers translate to 404.
var ErrCaptureNotFound = errors.New("capture not found")

// ErrCameraLPRConflict is returned by InsertCamera / UpdateCamera when
// the operation would create a second is_lpr=true row for the same
// device. The DB's partial unique index enforces single-LPR-per-device
// per ADR-030 § 1; the API translates this into HTTP 409.
var ErrCameraLPRConflict = errors.New("camera LPR conflict")

// BootstrapVerifier validates the enrollment bootstrap key. The registry
// depends on the interface; bootstrap.Verifier is the implementation, which
// refreshes the key from Secrets Manager on a mismatch (ADR-017).
type BootstrapVerifier interface {
	Verify(ctx context.Context, presented string) bool
}

type Config struct {
	BootstrapVerifier BootstrapVerifier
}

type Registry struct {
	pool *pgxpool.Pool
	iot  iotprovisioner.Provisioner
	cfg  Config
}

func New(pool *pgxpool.Pool, iot iotprovisioner.Provisioner, cfg Config) *Registry {
	return &Registry{pool: pool, iot: iot, cfg: cfg}
}

type EnrollInput struct {
	BootstrapKey string
	Hostname     string
	HardwareUUID string
	HardwareKind string
	OSVersion    string
	AgentVersion string
}

type EnrollOutput struct {
	DeviceID          string
	MtlsCertPEM       string
	MtlsPrivateKeyPEM string
	IoTThingARN       string
	MtlsCertExpiresAt time.Time
}

// Device is the row returned by GetByID. LastSeen is the raw last_seen
// column (nil until the first heartbeat lands); IsOnline is the stored
// presence state maintained by cp-ingest's ingesters and sweeper.
// PresenceChangedAt is when IsOnline last flipped. MtlsCertExpiresAt is the
// notAfter of the per-device mTLS cert minted at enrollment (nil only for
// rows that predate migration 006).
// DeviceService is one (device, service_name) row from the device_services
// table. Combines the agent's observation (state, state_since) with cp's
// receive timestamp (last_reported); the API handler maps it to its
// public JSON shape.
type DeviceService struct {
	Name         string
	State        service.State
	StateSince   time.Time
	LastReported time.Time
}

// DeviceHealthProbe is the read-side projection of one row in
// device_health_probes (#19). Status is the agent-decided colour;
// State the OS-agnostic signal token; Details the structured payload.
type DeviceHealthProbe struct {
	Name           string
	Status         string
	State          string
	Details        map[string]any
	LastObservedAt time.Time
}

// FleetAlerts is the #21 fleet-wide roll-up of unhealthy signals, grouped
// by type. It is alert-only: a ProbeAlert appears only for a probe with at
// least one red or yellow device, and a ServiceAlert only for a service with
// at least one stopped device. The device-id lists let the UI drill down to
// the affected boxes without a second request.
type FleetAlerts struct {
	Probes   []ProbeAlert
	Services []ServiceAlert
}

// ProbeAlert is the set of devices currently red and/or yellow on one probe.
// Both lists carry device ids, sorted, so the result is deterministic.
type ProbeAlert struct {
	ProbeName string
	Red       []string
	Yellow    []string
}

// ServiceAlert is the set of devices currently reporting one service stopped.
type ServiceAlert struct {
	ServiceName string
	Stopped     []string
}

type Device struct {
	ID                string
	Hostname          string
	HardwareUUID      string
	HardwareKind      string
	OSVersion         string
	AgentVersion      string
	IoTThingARN       string
	LastSeen          *time.Time
	IsOnline          bool
	PresenceChangedAt *time.Time
	MtlsCertExpiresAt *time.Time
	EnrolledAt        time.Time
	// SiteID is the local UUID of the assigned site, mirrored from the
	// devices.site_id column. Nil for an unassigned device. The
	// dashboard's EditDeploymentModal reads it to pre-select the
	// current site in the picker (avoiding a fragile name-match).
	SiteID *string
	// SiteName and ClientName are resolved by GetByID and List via the site
	// model; nil for a device with no site assigned.
	SiteName   *string
	ClientName *string
	// AssetNumber is the fleet-tracking identifier set during install
	// (migration 014). Nil until install-module 11 starts shipping it.
	AssetNumber *string
	// LanIP is the device's primary RFC1918 IPv4 address (migration
	// 018, issue #14). Populated on each heartbeat via
	// UpdateHeartbeatNetwork; nil until the first post-rollout
	// heartbeat lands. Used by the dashboard's "Copy LAN URL"
	// fallback for the Verify-angle deep-link.
	LanIP *string
	// TailscaleIP is the device's CGNAT (100.64.0.0/10) IPv4
	// (migration 018, issue #14). Nil for devices that aren't on a
	// tailnet or whose agent predates the rollout.
	TailscaleIP *string
	// TailscaleName is the device's MagicDNS name from
	// `tailscale status --json` Self.DNSName, trailing dot stripped
	// (migration 018, issue #14). The dashboard's edgePreviewURL
	// prefers this over Hostname so the Verify-angle button resolves
	// even when device.hostname diverged from the tailnet name (the
	// bench-Mac drift case 2026-05-26).
	TailscaleName *string
	// DesiredAgentVersion is the fleet-update rollout target (migration
	// 023, issue #40, ADR-035 §1/§4). Nil = untargeted. Rollout state is
	// derived by comparing it against AgentVersion (the reported side);
	// CP pushes agent.update until the two converge.
	DesiredAgentVersion *string

	// RolledBackVersion is the version the resident wrapper most recently
	// reverted after a failed health gate (migration 024). The agent reports
	// it in its heartbeat. Nil = no rollback reported. The rollout view shows
	// "rolled_back" when this equals DesiredAgentVersion on an un-converged
	// device — it tried the desired version and reverted.
	RolledBackVersion *string

	// SnapshotCadence is the per-device scheduled-snapshot frequency
	// (migration 025, #9): "off" | "daily" | "weekly", default "weekly". The
	// agent's snapshot scheduler reads it (pushed via config.update in a later
	// slice); the dashboard's device page lets staff change it.
	SnapshotCadence string

	// ALPRLicenseSet reports whether a per-device Plate Recognizer license is
	// configured (migration 026, #84). The license itself is a secret and is
	// deliberately NOT carried on Device — reads expose only
	// `alpr_license IS NOT NULL` so the dashboard shows set/not-set without the
	// value ever leaving the CP. Commission reads the raw value via
	// GetALPRLicense; staff sets it via SetALPRLicense.
	ALPRLicenseSet bool
}

func (r *Registry) GetByID(ctx context.Context, id string) (Device, error) {
	// Every device read is site-scoped (PRD § AuthZ): the scope middleware
	// resolves the operator's SiteFilter into context. A read with no scope
	// fails closed — it sees nothing.
	filter, ok := authz.ScopeFromContext(ctx)
	if !ok {
		return Device{}, ErrDeviceNotFound
	}
	sql, args := authz.ScopedDeviceQuery(filter, `
		SELECT devices.id, devices.hostname, devices.hardware_uuid, devices.hardware_kind,
		       devices.os_version, devices.agent_version, devices.iot_thing_arn,
		       devices.last_seen, devices.is_online, devices.presence_changed_at,
		       devices.mtls_cert_expires_at, devices.enrolled_at,
		       devices.site_id::text AS site_id,
		       s.name AS site_name, c.name AS client_name,
		       devices.asset_number,
		       devices.lan_ip, devices.tailscale_ip, devices.tailscale_name,
		       devices.desired_agent_version, devices.rolled_back_version,
		       devices.snapshot_cadence,
		       (devices.alpr_license IS NOT NULL) AS alpr_license_set
		FROM devices
		LEFT JOIN sites s ON s.id = devices.site_id
		LEFT JOIN clients c ON c.id = s.client_id
		WHERE devices.id = $1
	`, id)
	var d Device
	err := r.pool.QueryRow(ctx, sql, args...).Scan(
		&d.ID, &d.Hostname, &d.HardwareUUID, &d.HardwareKind,
		&d.OSVersion, &d.AgentVersion, &d.IoTThingARN,
		&d.LastSeen, &d.IsOnline, &d.PresenceChangedAt,
		&d.MtlsCertExpiresAt, &d.EnrolledAt,
		&d.SiteID,
		&d.SiteName, &d.ClientName,
		&d.AssetNumber,
		&d.LanIP, &d.TailscaleIP, &d.TailscaleName,
		&d.DesiredAgentVersion, &d.RolledBackVersion,
		&d.SnapshotCadence,
		&d.ALPRLicenseSet,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Device{}, ErrDeviceNotFound
		}
		return Device{}, fmt.Errorf("get device: %w", err)
	}
	return d, nil
}

// List returns the devices visible to the operator whose SiteFilter is in
// ctx, ordered by hostname. A read with no resolved scope fails closed,
// returning an empty list.
func (r *Registry) List(ctx context.Context) ([]Device, error) {
	filter, ok := authz.ScopeFromContext(ctx)
	if !ok {
		return nil, nil
	}
	sql, args := authz.ScopedDeviceQuery(filter, `
		SELECT devices.id, devices.hostname, devices.hardware_uuid, devices.hardware_kind,
		       devices.os_version, devices.agent_version, devices.iot_thing_arn,
		       devices.last_seen, devices.is_online, devices.presence_changed_at,
		       devices.mtls_cert_expires_at, devices.enrolled_at,
		       devices.site_id::text AS site_id,
		       s.name AS site_name, c.name AS client_name,
		       devices.asset_number,
		       devices.lan_ip, devices.tailscale_ip, devices.tailscale_name,
		       devices.desired_agent_version, devices.rolled_back_version,
		       devices.snapshot_cadence,
		       (devices.alpr_license IS NOT NULL) AS alpr_license_set
		FROM devices
		LEFT JOIN sites s ON s.id = devices.site_id
		LEFT JOIN clients c ON c.id = s.client_id
		WHERE true
	`)
	rows, err := r.pool.Query(ctx, sql+" ORDER BY devices.hostname", args...)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(
			&d.ID, &d.Hostname, &d.HardwareUUID, &d.HardwareKind,
			&d.OSVersion, &d.AgentVersion, &d.IoTThingARN,
			&d.LastSeen, &d.IsOnline, &d.PresenceChangedAt,
			&d.MtlsCertExpiresAt, &d.EnrolledAt,
			&d.SiteID,
			&d.SiteName, &d.ClientName,
			&d.AssetNumber,
			&d.LanIP, &d.TailscaleIP, &d.TailscaleName,
			&d.DesiredAgentVersion, &d.RolledBackVersion,
			&d.SnapshotCadence,
			&d.ALPRLicenseSet,
		); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}
	return devices, nil
}

// DeleteDevice removes a device row — the CP-side half of decommissioning
// (the AWS IoT thing + cert are deleted out-of-band per the decommission
// runbook). Child rows (services, health probes, cameras, log tails, network
// scans) cascade via their ON DELETE CASCADE FKs. A non-UUID or unknown id
// returns ErrDeviceNotFound. Staff-only at the API layer.
func (r *Registry) DeleteDevice(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrDeviceNotFound
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM devices WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// UpdateLastSeen records a heartbeat: it stamps last_seen and marks the
// device online, moving presence_changed_at only when the device was
// previously offline — a steady-state heartbeat does not disturb it. An id
// that matches no row — including a non-UUID — returns ErrDeviceNotFound, so
// the presence ingester can DLQ an unknown-device heartbeat instead of
// looping on it.
func (r *Registry) UpdateLastSeen(ctx context.Context, deviceID string, at time.Time) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET last_seen = $2,
		    is_online = true,
		    presence_changed_at = CASE WHEN is_online THEN presence_changed_at ELSE $2 END,
		    updated_at = now()
		WHERE id = $1
	`, deviceID, at)
	if err != nil {
		return fmt.Errorf("update last_seen: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// UpdateHeartbeatNetwork records the three network fields the
// agent publishes on each heartbeat (issue #14): lan_ip,
// tailscale_ip, tailscale_name. Each argument is *string with
// conditional-update semantics — a nil pointer means "the agent
// didn't publish this field on this heartbeat; don't touch the
// stored value." A non-nil pointer overwrites (last-wins).
//
// This separation matters because an agent that temporarily loses
// tailnet visibility omits tailscale_* from its envelope; we don't
// want a brief outage to NULL out the dashboard's stored name and
// break the Verify-angle URL. Per-heartbeat last-wins on the
// fields that are present.
//
// An id matching no row — including a non-UUID — returns
// ErrDeviceNotFound so the ingest handler can DLQ rather than loop.
func (r *Registry) UpdateHeartbeatNetwork(ctx context.Context, deviceID string, lanIP, tailscaleIP, tailscaleName *string) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	// COALESCE(new, stored) is the conditional-update idiom: when
	// the parameter is NULL (the *string was nil), the column keeps
	// its prior value. When the parameter is a non-NULL text, it
	// wins. updated_at advances unconditionally on a real
	// network-info heartbeat (the row was visited).
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET lan_ip         = COALESCE($2, lan_ip),
		    tailscale_ip   = COALESCE($3, tailscale_ip),
		    tailscale_name = COALESCE($4, tailscale_name),
		    updated_at     = now()
		WHERE id = $1
	`, deviceID, lanIP, tailscaleIP, tailscaleName)
	if err != nil {
		return fmt.Errorf("update heartbeat network: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// SetDeployment assigns a device to a site (or clears the
// assignment) and sets/clears the asset_number in a single update.
// PUT-semantics: both fields are always written — pass the prior
// values if you mean to keep them. nil clears the column.
//
// A non-nil siteID is pre-checked against the local mirror so an
// unknown-site call returns ErrSiteNotFound (→ 400) instead of a
// raw FK violation (→ 500). A missing device returns ErrDeviceNotFound.
func (r *Registry) SetDeployment(ctx context.Context, deviceID string, siteID *string, assetNumber *string) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	if siteID != nil {
		if _, err := uuid.Parse(*siteID); err != nil {
			return ErrSiteNotFound
		}
		var exists bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM sites WHERE id = $1)`, *siteID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check site exists: %w", err)
		}
		if !exists {
			return ErrSiteNotFound
		}
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET site_id      = $2,
		    asset_number = $3,
		    updated_at   = now()
		WHERE id = $1
	`, deviceID, siteID, assetNumber)
	if err != nil {
		return fmt.Errorf("update deployment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// RecordServiceStates persists a service-status report: per-(device,
// service_name) UPSERT of the agent's observed state + agent's
// best-effort state_since + cp-side ingest timestamp. An empty slice
// is a valid no-op. An id matching no row — including a non-UUID —
// returns ErrDeviceNotFound so the ingester can DLQ a late report from
// a decommissioned device instead of looping.
//
// The per-service UPSERTs run inside a single transaction so a partial
// failure leaves storage in its prior state. At Phase 2 fleet scale
// the allow-list is small (~5–10 services per device) so the loop is
// cheap; if it ever stops being so we switch to a bulk INSERT with
// unnest() — same semantics, no test changes needed.
func (r *Registry) RecordServiceStates(ctx context.Context, deviceID string, states []servicestatus.ServiceState, reportedAt time.Time) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id = $1)`, deviceID).Scan(&exists); err != nil {
		return fmt.Errorf("device exists check: %w", err)
	}
	if !exists {
		return ErrDeviceNotFound
	}
	if len(states) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, s := range states {
		if _, err := tx.Exec(ctx, `
			INSERT INTO device_services (device_id, service_name, state, state_since, last_reported)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (device_id, service_name) DO UPDATE SET
				state         = EXCLUDED.state,
				state_since   = EXCLUDED.state_since,
				last_reported = EXCLUDED.last_reported
		`, deviceID, s.Name, string(s.State), s.StateSince, reportedAt); err != nil {
			return fmt.Errorf("upsert device_service %s: %w", s.Name, err)
		}
	}
	return tx.Commit(ctx)
}

// RecordHealthProbes persists a fleet-health-probe report (#19): per-
// (device, probe_name) UPSERT of the agent-decided colour (status), the
// OS-agnostic signal token (state), the structured details payload, and
// the cp-side ingest timestamp. An empty slice is a valid no-op. An id
// matching no row — including a non-UUID — returns ErrDeviceNotFound so
// the ingester can DLQ a late report from a decommissioned device.
//
// Per-probe UPSERTs run in one transaction so a partial failure leaves
// storage unchanged. The probe set is small (seven in slice 1) so the
// loop is cheap.
func (r *Registry) RecordHealthProbes(ctx context.Context, deviceID string, results []healthprobes.Result, observedAt time.Time) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id = $1)`, deviceID).Scan(&exists); err != nil {
		return fmt.Errorf("device exists check: %w", err)
	}
	if !exists {
		return ErrDeviceNotFound
	}
	if len(results) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, p := range results {
		details := p.Details
		if details == nil {
			details = map[string]any{}
		}
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal details for probe %s: %w", p.Name, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO device_health_probes (device_id, probe_name, status, state, details, last_observed_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (device_id, probe_name) DO UPDATE SET
				status           = EXCLUDED.status,
				state            = EXCLUDED.state,
				details          = EXCLUDED.details,
				last_observed_at = EXCLUDED.last_observed_at
		`, deviceID, p.Name, string(p.Status), p.State, detailsJSON, observedAt); err != nil {
			return fmt.Errorf("upsert device_health_probe %s: %w", p.Name, err)
		}
	}
	return tx.Commit(ctx)
}

// ListServices returns the per-service rows for a device, ordered by
// service_name. An empty (but non-nil) slice for a device that has
// never reported. A non-UUID deviceID returns an empty slice rather
// than ErrDeviceNotFound — the per-device API surface treats it as
// "no services" so the page still renders identity + presence even if
// the URL path was mangled.
func (r *Registry) ListServices(ctx context.Context, deviceID string) ([]DeviceService, error) {
	out := []DeviceService{}
	if _, err := uuid.Parse(deviceID); err != nil {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT service_name, state, state_since, last_reported
		FROM device_services
		WHERE device_id = $1
		ORDER BY service_name
	`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list device_services: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ds DeviceService
		var stateStr string
		if err := rows.Scan(&ds.Name, &stateStr, &ds.StateSince, &ds.LastReported); err != nil {
			return nil, fmt.Errorf("scan device_service: %w", err)
		}
		ds.State = service.State(stateStr)
		out = append(out, ds)
	}
	return out, rows.Err()
}

// ListHealthProbes returns the per-probe rows for a device, ordered by
// probe_name. An empty (non-nil) slice for a device that has never
// reported. A non-UUID deviceID returns an empty slice rather than an
// error — the per-device API treats it as "no probes" so the page still
// renders (mirrors ListServices).
func (r *Registry) ListHealthProbes(ctx context.Context, deviceID string) ([]DeviceHealthProbe, error) {
	out := []DeviceHealthProbe{}
	if _, err := uuid.Parse(deviceID); err != nil {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT probe_name, status, state, details, last_observed_at
		FROM device_health_probes
		WHERE device_id = $1
		ORDER BY probe_name
	`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list device_health_probes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p DeviceHealthProbe
		var detailsJSON []byte
		if err := rows.Scan(&p.Name, &p.Status, &p.State, &detailsJSON, &p.LastObservedAt); err != nil {
			return nil, fmt.Errorf("scan device_health_probe: %w", err)
		}
		if len(detailsJSON) > 0 {
			if err := json.Unmarshal(detailsJSON, &p.Details); err != nil {
				return nil, fmt.Errorf("unmarshal probe details: %w", err)
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FleetAlerts returns the site-scoped fleet roll-up of unhealthy signals
// (#21): devices currently red/yellow per probe and stopped per service,
// grouped by type. It is alert-only — healthy probes and running services
// contribute no entry. Both reads route through ScopedDeviceQuery so a
// site-scoped operator only sees devices at their allowlisted sites; a read
// with no resolved scope fails closed, returning an empty roll-up. The
// ORDER BY makes each type's rows contiguous and its device-id list sorted,
// so grouping in one pass yields a deterministic result.
func (r *Registry) FleetAlerts(ctx context.Context) (FleetAlerts, error) {
	out := FleetAlerts{Probes: []ProbeAlert{}, Services: []ServiceAlert{}}
	filter, ok := authz.ScopeFromContext(ctx)
	if !ok {
		return out, nil
	}

	probeSQL, probeArgs := authz.ScopedDeviceQuery(filter, `
		SELECT dhp.probe_name, dhp.status, dhp.device_id::text
		FROM device_health_probes dhp
		JOIN devices ON devices.id = dhp.device_id
		WHERE dhp.status IN ('red', 'yellow')
	`)
	probeRows, err := r.pool.Query(ctx, probeSQL+" ORDER BY dhp.probe_name, dhp.device_id", probeArgs...)
	if err != nil {
		return out, fmt.Errorf("query probe alerts: %w", err)
	}
	defer probeRows.Close()
	probeIdx := map[string]int{}
	for probeRows.Next() {
		var name, status, deviceID string
		if err := probeRows.Scan(&name, &status, &deviceID); err != nil {
			return out, fmt.Errorf("scan probe alert: %w", err)
		}
		i, seen := probeIdx[name]
		if !seen {
			i = len(out.Probes)
			out.Probes = append(out.Probes, ProbeAlert{ProbeName: name})
			probeIdx[name] = i
		}
		if status == "red" {
			out.Probes[i].Red = append(out.Probes[i].Red, deviceID)
		} else {
			out.Probes[i].Yellow = append(out.Probes[i].Yellow, deviceID)
		}
	}
	if err := probeRows.Err(); err != nil {
		return out, fmt.Errorf("iterate probe alerts: %w", err)
	}
	probeRows.Close()

	svcSQL, svcArgs := authz.ScopedDeviceQuery(filter, `
		SELECT ds.service_name, ds.device_id::text
		FROM device_services ds
		JOIN devices ON devices.id = ds.device_id
		WHERE ds.state = 'stopped'
	`)
	svcRows, err := r.pool.Query(ctx, svcSQL+" ORDER BY ds.service_name, ds.device_id", svcArgs...)
	if err != nil {
		return out, fmt.Errorf("query service alerts: %w", err)
	}
	defer svcRows.Close()
	svcIdx := map[string]int{}
	for svcRows.Next() {
		var name, deviceID string
		if err := svcRows.Scan(&name, &deviceID); err != nil {
			return out, fmt.Errorf("scan service alert: %w", err)
		}
		i, seen := svcIdx[name]
		if !seen {
			i = len(out.Services)
			out.Services = append(out.Services, ServiceAlert{ServiceName: name})
			svcIdx[name] = i
		}
		out.Services[i].Stopped = append(out.Services[i].Stopped, deviceID)
	}
	if err := svcRows.Err(); err != nil {
		return out, fmt.Errorf("iterate service alerts: %w", err)
	}

	return out, nil
}

// FleetCameraRollup is the #152 fleet-wide camera reachability roll-up that
// backs the Overview's Cameras gauge + Camera alerts panel: the online/total
// counts plus the currently-offline cameras with enough context (device, site,
// since-when) to triage. Cameras whose status is "unknown" (never probed) count
// toward Total but are neither Online nor in Offline.
type FleetCameraRollup struct {
	Total   int
	Online  int
	Offline []OfflineCamera
}

// OfflineCamera is one currently-offline camera in the fleet roll-up.
// StatusChangedAt is when it went offline (nil if never recorded).
type OfflineCamera struct {
	CameraID        string
	Label           string
	DeviceID        string
	Hostname        string
	SiteName        *string
	StatusChangedAt *time.Time
}

// FleetCameras returns the site-scoped fleet camera roll-up (#152). It routes
// through ScopedDeviceQuery so a site-scoped operator only sees cameras at their
// allowlisted sites; a read with no resolved scope fails closed (empty roll-up).
// The offline list is ordered longest-outage-first (oldest status_changed_at),
// so the most urgent outage sorts to the top of the alerts panel.
func (r *Registry) FleetCameras(ctx context.Context) (FleetCameraRollup, error) {
	out := FleetCameraRollup{Offline: []OfflineCamera{}}
	filter, ok := authz.ScopeFromContext(ctx)
	if !ok {
		return out, nil
	}

	sql, args := authz.ScopedDeviceQuery(filter, `
		SELECT dc.status, dc.camera_id, dc.label, dc.device_id::text,
		       devices.hostname, s.name, dc.status_changed_at
		FROM device_cameras dc
		JOIN devices ON devices.id = dc.device_id
		LEFT JOIN sites s ON s.id = devices.site_id
		WHERE true
	`)
	rows, err := r.pool.Query(ctx, sql+" ORDER BY dc.status_changed_at ASC NULLS LAST, dc.camera_id", args...)
	if err != nil {
		return out, fmt.Errorf("query fleet cameras: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var cam OfflineCamera
		if err := rows.Scan(&status, &cam.CameraID, &cam.Label, &cam.DeviceID,
			&cam.Hostname, &cam.SiteName, &cam.StatusChangedAt); err != nil {
			return out, fmt.Errorf("scan fleet camera: %w", err)
		}
		out.Total++
		switch status {
		case "online":
			out.Online++
		case "offline":
			out.Offline = append(out.Offline, cam)
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterate fleet cameras: %w", err)
	}
	return out, nil
}

// Capture is a row in device_captures (#8): one uploaded artifact's index
// entry. The bytes live in S3 at S3Key; Metadata is the per-kind payload
// (e.g. {"camera_id": "cam1"} for a snapshot).
type Capture struct {
	ID          string
	DeviceID    string
	Kind        string
	S3Key       string
	ContentType string
	SizeBytes   int64
	Metadata    map[string]any
	CreatedAt   time.Time
}

// CaptureInput is the create-time shape; id + created_at are server-assigned.
type CaptureInput struct {
	DeviceID    string
	Kind        string
	S3Key       string
	ContentType string
	SizeBytes   int64
	Metadata    map[string]any
}

// InsertCapture indexes a freshly-uploaded artifact and returns the stored
// row (assigned id + created_at). Called by the cmd-result handler after the
// agent confirms its S3 PUT — system context, not site-scoped.
func (r *Registry) InsertCapture(ctx context.Context, in CaptureInput) (Capture, error) {
	md := in.Metadata
	if md == nil {
		md = map[string]any{}
	}
	mdJSON, err := json.Marshal(md)
	if err != nil {
		return Capture{}, fmt.Errorf("marshal capture metadata: %w", err)
	}
	var c Capture
	var outMD []byte
	err = r.pool.QueryRow(ctx, `
		INSERT INTO device_captures (device_id, kind, s3_key, content_type, size_bytes, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, device_id::text, kind, s3_key, content_type, size_bytes, metadata, created_at
	`, in.DeviceID, in.Kind, in.S3Key, in.ContentType, in.SizeBytes, mdJSON).
		Scan(&c.ID, &c.DeviceID, &c.Kind, &c.S3Key, &c.ContentType, &c.SizeBytes, &outMD, &c.CreatedAt)
	if err != nil {
		return Capture{}, fmt.Errorf("insert device_capture: %w", err)
	}
	if err := json.Unmarshal(outMD, &c.Metadata); err != nil {
		return Capture{}, fmt.Errorf("unmarshal capture metadata: %w", err)
	}
	return c, nil
}

// ListCaptures returns a device's captures newest-first, site-scoped. An
// empty kind returns all kinds; otherwise it filters to that kind. A read
// with no resolved scope fails closed (empty).
func (r *Registry) ListCaptures(ctx context.Context, deviceID, kind string) ([]Capture, error) {
	filter, ok := authz.ScopeFromContext(ctx)
	if !ok {
		return nil, nil
	}
	sql, args := authz.ScopedDeviceQuery(filter, `
		SELECT dc.id::text, dc.device_id::text, dc.kind, dc.s3_key, dc.content_type,
		       dc.size_bytes, dc.metadata, dc.created_at
		FROM device_captures dc
		JOIN devices ON devices.id = dc.device_id
		WHERE dc.device_id = $1 AND ($2 = '' OR dc.kind = $2)
	`, deviceID, kind)
	rows, err := r.pool.Query(ctx, sql+" ORDER BY dc.created_at DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("list device_captures: %w", err)
	}
	defer rows.Close()
	out := []Capture{}
	for rows.Next() {
		c, err := scanCapture(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCapture returns one capture by id, site-scoped (so an operator can't
// mint a signed URL for a device outside their allowlist). ErrCaptureNotFound
// when missing or out of scope.
func (r *Registry) GetCapture(ctx context.Context, id string) (Capture, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Capture{}, ErrCaptureNotFound
	}
	filter, ok := authz.ScopeFromContext(ctx)
	if !ok {
		return Capture{}, ErrCaptureNotFound
	}
	sql, args := authz.ScopedDeviceQuery(filter, `
		SELECT dc.id::text, dc.device_id::text, dc.kind, dc.s3_key, dc.content_type,
		       dc.size_bytes, dc.metadata, dc.created_at
		FROM device_captures dc
		JOIN devices ON devices.id = dc.device_id
		WHERE dc.id = $1
	`, id)
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return Capture{}, fmt.Errorf("get device_capture: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Capture{}, fmt.Errorf("get device_capture: %w", err)
		}
		return Capture{}, ErrCaptureNotFound
	}
	return scanCapture(rows)
}

// DeleteSnapshotsOlderThan prunes snapshot capture rows older than cutoff (#9
// retention). The captures bucket's S3 lifecycle expires the objects on the
// same 90-day horizon; this keeps the index in step so the history view never
// lists a row whose object has already been deleted. System context (a
// cp-ingest sweeper), not site-scoped. Returns the number deleted.
func (r *Registry) DeleteSnapshotsOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM device_captures
		WHERE kind = 'snapshot' AND created_at < $1
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete stale snapshots: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func scanCapture(rows pgx.Rows) (Capture, error) {
	var c Capture
	var md []byte
	if err := rows.Scan(&c.ID, &c.DeviceID, &c.Kind, &c.S3Key, &c.ContentType, &c.SizeBytes, &md, &c.CreatedAt); err != nil {
		return Capture{}, fmt.Errorf("scan device_capture: %w", err)
	}
	if len(md) > 0 {
		if err := json.Unmarshal(md, &c.Metadata); err != nil {
			return Capture{}, fmt.Errorf("unmarshal capture metadata: %w", err)
		}
	}
	return c, nil
}

// ServiceConfig is the per-device override of the agent's service
// allow-list + reporting cadence (Phase 2 slice 2). All four fields are
// nilable — a fresh device row has all-nil semantics. LastApplied* are
// stamped by the cmd-result handler when the agent ACKs a config.update;
// nil = the override has been set but not yet confirmed applied (or no
// override has ever been set).
//
// AllowListOverride distinguishes nil (no override; agent uses its
// bundled list) from an empty slice ("track nothing"). The JSONB column
// stores either SQL NULL or a JSON array literal — including the empty
// literal `[]`.
type ServiceConfig struct {
	AllowListOverride        *[]string
	IntervalOverride         *string
	LastAppliedAt            *time.Time
	LastAppliedCorrelationID *string
}

// GetServiceConfig returns the per-device override + last-applied
// tracking. A non-UUID deviceID returns ErrDeviceNotFound. A row that
// exists but has never had an override set returns a zero-valued
// ServiceConfig (all fields nil).
//
// Site-scoped via ScopedDeviceQuery — an operator who is out of scope
// for the device sees ErrDeviceNotFound (fail-closed); the SQL gate
// flags any devices SELECT that bypasses this path.
func (r *Registry) GetServiceConfig(ctx context.Context, deviceID string) (ServiceConfig, error) {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ServiceConfig{}, ErrDeviceNotFound
	}
	filter, ok := authz.ScopeFromContext(ctx)
	if !ok {
		return ServiceConfig{}, ErrDeviceNotFound
	}
	sql, args := authz.ScopedDeviceQuery(filter, `
		SELECT service_allow_list_override,
		       service_status_interval_override,
		       service_config_last_applied_at,
		       service_config_last_applied_corr_id
		FROM devices
		WHERE id = $1
	`, deviceID)
	var (
		listRaw []byte
		cfg     ServiceConfig
	)
	err := r.pool.QueryRow(ctx, sql, args...).Scan(&listRaw, &cfg.IntervalOverride, &cfg.LastAppliedAt, &cfg.LastAppliedCorrelationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceConfig{}, ErrDeviceNotFound
		}
		return ServiceConfig{}, fmt.Errorf("get service config: %w", err)
	}
	if listRaw != nil {
		var list []string
		if err := json.Unmarshal(listRaw, &list); err != nil {
			return ServiceConfig{}, fmt.Errorf("decode allow-list override: %w", err)
		}
		// Preserve "[]" as an empty (non-nil) slice — operator
		// explicitly chose to track nothing.
		if list == nil {
			list = []string{}
		}
		cfg.AllowListOverride = &list
	}
	return cfg, nil
}

// AgentVersionState returns a device's reported + desired agent version for
// the reconnect reconcile check (issue #40). Unlike GetByID it is not
// site-scoped: cp-ingest workers run without an operator scope, same as the
// other ingest-side reads.
func (r *Registry) AgentVersionState(ctx context.Context, deviceID string) (string, *string, error) {
	if _, err := uuid.Parse(deviceID); err != nil {
		return "", nil, ErrDeviceNotFound
	}
	var reported string
	var desired *string
	err := r.pool.QueryRow(ctx, `
		SELECT agent_version, desired_agent_version FROM devices WHERE id = $1
	`, deviceID).Scan(&reported, &desired)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrDeviceNotFound
		}
		return "", nil, fmt.Errorf("agent version state: %w", err)
	}
	return reported, desired, nil
}

// RecordReportedAgentVersion persists the heartbeat-reported agent version
// (issue #40) — after enrollment seeds agent_version, this is what keeps the
// reported side of desired-vs-reported fresh as updates land. It returns the
// device's desired version (nil = untargeted) so the heartbeat ingester can
// make the reconcile decision without a second round trip.
func (r *Registry) RecordReportedAgentVersion(ctx context.Context, deviceID, version string) (*string, error) {
	if _, err := uuid.Parse(deviceID); err != nil {
		return nil, ErrDeviceNotFound
	}
	var desired *string
	err := r.pool.QueryRow(ctx, `
		UPDATE devices
		SET agent_version = $2,
		    updated_at    = now()
		WHERE id = $1
		RETURNING desired_agent_version
	`, deviceID, version).Scan(&desired)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDeviceNotFound
		}
		return nil, fmt.Errorf("record reported agent version: %w", err)
	}
	return desired, nil
}

// RecordRolledBackVersion persists the version the resident wrapper most
// recently reverted on a device (migration 024), reported in the heartbeat.
// It's a last-wins write — the agent always reports the latest rollback — so
// the rollout view can flag a device that tried the desired version and
// reverted. Unknown / non-UUID ids are reported as ErrDeviceNotFound.
func (r *Registry) RecordRolledBackVersion(ctx context.Context, deviceID, version string) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET rolled_back_version = $2, updated_at = now()
		WHERE id = $1
	`, deviceID, version)
	if err != nil {
		return fmt.Errorf("record rolled back version: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// SetDesiredAgentVersion stamps the fleet-update rollout target on a set of
// devices (issue #40, ADR-035 §1). Non-UUID or unknown ids in the set are
// skipped, not an error — the returned count of stamped rows is the caller's
// signal (the API layer rejects a target set that matched nothing). Last-wins
// on re-target: canary promotion and abort are both just another set.
// SnapshotCadences is the closed set of valid scheduled-snapshot cadences (#9),
// mirroring the migration-025 CHECK constraint. ValidSnapshotCadence guards the
// API before a write.
var SnapshotCadences = map[string]bool{"off": true, "daily": true, "weekly": true}

// ValidSnapshotCadence reports whether c is an accepted cadence.
func ValidSnapshotCadence(c string) bool { return SnapshotCadences[c] }

// SetSnapshotCadence updates a device's scheduled-snapshot cadence (#9). The
// caller (the snapshot-config PUT) site-scopes via a prior GetByID; the cadence
// string is validated by ValidSnapshotCadence before this runs. ErrDeviceNotFound
// when the id matches no row.
func (r *Registry) SetSnapshotCadence(ctx context.Context, deviceID, cadence string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET snapshot_cadence = $2,
		    updated_at        = now()
		WHERE id = $1
	`, deviceID, cadence)
	if err != nil {
		return fmt.Errorf("set snapshot cadence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

func (r *Registry) SetDesiredAgentVersion(ctx context.Context, deviceIDs []string, version string) (int, error) {
	valid := make([]string, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		if _, err := uuid.Parse(id); err == nil {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET desired_agent_version = $2,
		    updated_at            = now()
		WHERE id = ANY($1::uuid[])
	`, valid, version)
	if err != nil {
		return 0, fmt.Errorf("set desired agent version: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SetServiceConfig writes the per-device override. nil for either
// pointer means "clear this override" (NULL in the column); a non-nil
// pointer is the value to store. An empty slice via &[]string{} is a
// meaningful override distinct from nil — see ServiceConfig.
//
// last_applied_* tracking is NOT touched by this method (it's the
// operator-intent write); the cmd-result handler updates those fields
// independently when the agent ACKs.
func (r *Registry) SetServiceConfig(ctx context.Context, deviceID string, allowList *[]string, interval *string) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	var listJSON any // any so pgx writes SQL NULL for nil, JSONB literal for []byte
	if allowList != nil {
		raw, err := json.Marshal(*allowList)
		if err != nil {
			return fmt.Errorf("encode allow-list override: %w", err)
		}
		listJSON = raw
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET service_allow_list_override      = $2,
		    service_status_interval_override = $3,
		    updated_at                       = now()
		WHERE id = $1
	`, deviceID, listJSON, interval)
	if err != nil {
		return fmt.Errorf("set service config: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// SetALPRLicense stores the per-device Plate Recognizer license (#84, ADR-036
// §5). The value is a secret — never logged; reads for the dashboard expose
// only ALPRLicenseSet. Commission reads the raw value via GetALPRLicense.
func (r *Registry) SetALPRLicense(ctx context.Context, deviceID, license string) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices SET alpr_license = $2, updated_at = now() WHERE id = $1
	`, deviceID, license)
	if err != nil {
		return fmt.Errorf("set alpr license: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// GetALPRLicense returns the raw per-device Plate Recognizer license, or "" if
// none is set. Used only by Commission (#91) to push the license to the device;
// never surfaced through the device read path.
func (r *Registry) GetALPRLicense(ctx context.Context, deviceID string) (string, error) {
	if _, err := uuid.Parse(deviceID); err != nil {
		return "", ErrDeviceNotFound
	}
	var license *string
	err := r.pool.QueryRow(ctx, `SELECT alpr_license FROM devices WHERE id = $1`, deviceID).Scan(&license)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrDeviceNotFound
		}
		return "", fmt.Errorf("get alpr license: %w", err)
	}
	if license == nil {
		return "", nil
	}
	return *license, nil
}

// SettingPlateRecognizerToken is the cp_settings key for the account-wide
// Plate Recognizer token (#84, ADR-036 §5).
const SettingPlateRecognizerToken = "plate_recognizer_token"

// cp_settings keys for fleet notifications (#96), all read/written through
// SetCPSetting/GetCPSetting. Enabled is "true"/"false"; Recipients is a JSON
// string array; TeamsWebhookURL is a write-only secret — the API never returns
// it raw, only whether it is set plus a host-only preview.
const (
	SettingNotificationsEnabled    = "notifications.enabled"
	SettingNotificationsRecipients = "notifications.email_recipients"
	SettingTeamsWebhookURL         = "notifications.teams_webhook_url"
)

// SetCPSetting upserts a CP-singleton setting (#84, migration 027). Values may
// be secret — callers must not log them.
func (r *Registry) SetCPSetting(ctx context.Context, key, value string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO cp_settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, key, value)
	if err != nil {
		return fmt.Errorf("set cp setting %q: %w", key, err)
	}
	return nil
}

// GetCPSetting returns a CP-singleton setting's value and whether it is set.
// Commission reads the PR token through this; the API never returns the raw
// value, only whether it is set.
func (r *Registry) GetCPSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.pool.QueryRow(ctx, `SELECT value FROM cp_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get cp setting %q: %w", key, err)
	}
	return value, true, nil
}

// RecordServiceConfigApplied stamps the (timestamp, correlation_id)
// of a config.update ACK on the device row (Phase 2 slice 2). The
// cp-ingest cmd-result handler calls this on every successful ACK;
// the dashboard reads the fields back via GetServiceConfig to render
// the EditServicesModal's "applied" badge. UPDATE semantics are
// latest-wins — a re-delivered ACK with the same (id, corr_id) is a
// no-op rewrite. A non-UUID or unknown deviceID returns
// ErrDeviceNotFound so the cmd-result handler can DLQ a late ACK
// from a decommissioned device.
func (r *Registry) RecordServiceConfigApplied(ctx context.Context, deviceID, correlationID string, at time.Time) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET service_config_last_applied_at      = $2,
		    service_config_last_applied_corr_id = $3,
		    updated_at                          = now()
		WHERE id = $1
	`, deviceID, at, correlationID)
	if err != nil {
		return fmt.Errorf("record service config applied: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// DeleteStaleDeviceServices removes rows from device_services whose
// last_reported is older than the threshold. Called by cp-ingest's
// DeviceServicesSweeper on a tick; the threshold should be set to
// ~3× the service-status reporting cadence so a single missed
// report (transient network glitch) doesn't drop a row.
//
// Rationale: when an operator removes a service from a device's
// allow-list via the dashboard (Phase 2 slice 2's EditServicesModal),
// the agent stops reporting on it, last_reported stops advancing,
// and after the threshold the row disappears from the Services panel.
func (r *Registry) DeleteStaleDeviceServices(ctx context.Context, olderThan time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM device_services WHERE last_reported < $1
	`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("delete stale device_services: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SetPresence records a device's online/offline state and the time it
// changed. Callers pass it only on a real transition (the Presence module
// reports which devices changed). An id matching no row — including a
// non-UUID — returns ErrDeviceNotFound.
func (r *Registry) SetPresence(ctx context.Context, deviceID string, online bool, at time.Time) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET is_online = $2, presence_changed_at = $3, updated_at = now()
		WHERE id = $1
	`, deviceID, online, at)
	if err != nil {
		return fmt.Errorf("set presence: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// ReconcileStalePresence is the DB-backed backstop for the in-memory presence
// sweeper (issue: stuck "online" with a stale last_seen). It flips is_online to
// false for every device that is still marked online but whose last_seen — AND
// last presence transition — are both older than staleBefore, stamping the
// transition at `now`. Returns the number of devices reconciled.
//
// Unlike the in-memory sweeper this reads the database, so it catches devices
// the sweeper never learned about (e.g. one that died ungracefully before a
// cp-ingest restart — no IoT "disconnected" event, no heartbeat to repopulate
// the in-memory model). The presence_changed_at guard protects a device that
// just reconnected via a lifecycle event but hasn't heartbeated yet: its
// transition is recent even though last_seen is still old, so it is left online
// until staleBefore catches up (by which point a healthy device has
// heartbeated and refreshed last_seen). last_seen itself is never modified — it
// remains the record of last contact.
func (r *Registry) ReconcileStalePresence(ctx context.Context, staleBefore, now time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET is_online = false, presence_changed_at = $2, updated_at = now()
		WHERE is_online = true
		  AND last_seen IS NOT NULL
		  AND last_seen < $1
		  AND (presence_changed_at IS NULL OR presence_changed_at < $1)
	`, staleBefore, now)
	if err != nil {
		return 0, fmt.Errorf("reconcile stale presence: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// LogTailRequest is the operator-initiated tail request shape — what
// CreateLogTailRequest persists. CorrelationID is the row PK and is
// minted CP-side; the agent echoes it on cmd-result so the
// CmdResultIngester can resolve the right row.
type LogTailRequest struct {
	CorrelationID  string
	DeviceID       string
	LogName        string
	LinesRequested int
	RequestedAt    time.Time
}

// LogTailCompletion is the success-path agent response shape.
// Truncated=true with TruncatedFrom means the agent had to cap the
// content to fit the MQTT envelope; the dashboard surfaces this.
type LogTailCompletion struct {
	CorrelationID string
	Content       string
	Truncated     bool
	TruncatedFrom int
	ReturnedAt    time.Time
}

// LogTailFailure is the error-path agent response shape. ErrorCode is
// a stable string (e.g. "log_tail.unknown_log", "log_tail.binary_file")
// the dashboard can match on; ErrorMessage is the human-readable
// elaboration.
type LogTailFailure struct {
	CorrelationID string
	ErrorCode     string
	ErrorMessage  string
	ReturnedAt    time.Time
}

// LogTail is the row shape returned by GetLogTail — combines the
// request fields with the (possibly nil) response fields.
type LogTail struct {
	CorrelationID  string
	DeviceID       string
	LogName        string
	LinesRequested int
	Status         string // pending | done | error
	Content        *string
	Truncated      bool
	TruncatedFrom  *int
	ErrorCode      *string
	ErrorMessage   *string
	RequestedAt    time.Time
	ReturnedAt     *time.Time
}

// CreateLogTailRequest persists a fresh "pending" row keyed by
// correlation_id. The CP API publishes the log.tail cmd immediately
// after; the agent's ACK lands as a Complete or Fail call from the
// cmd-result handler.
func (r *Registry) CreateLogTailRequest(ctx context.Context, req LogTailRequest) error {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO device_log_tails (
			correlation_id, device_id, log_name, lines_requested,
			status, requested_at
		) VALUES ($1, $2, $3, $4, 'pending', $5)
	`, req.CorrelationID, req.DeviceID, req.LogName, req.LinesRequested, req.RequestedAt); err != nil {
		return fmt.Errorf("insert device_log_tails: %w", err)
	}
	return nil
}

// GetLogTail returns one row by correlation_id, or ErrLogTailNotFound.
func (r *Registry) GetLogTail(ctx context.Context, correlationID string) (LogTail, error) {
	var t LogTail
	err := r.pool.QueryRow(ctx, `
		SELECT correlation_id, device_id, log_name, lines_requested,
		       status, content, truncated, truncated_from,
		       error_code, error_message, requested_at, returned_at
		FROM device_log_tails
		WHERE correlation_id = $1
	`, correlationID).Scan(
		&t.CorrelationID, &t.DeviceID, &t.LogName, &t.LinesRequested,
		&t.Status, &t.Content, &t.Truncated, &t.TruncatedFrom,
		&t.ErrorCode, &t.ErrorMessage, &t.RequestedAt, &t.ReturnedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LogTail{}, ErrLogTailNotFound
		}
		return LogTail{}, fmt.Errorf("get log tail: %w", err)
	}
	return t, nil
}

// CompleteLogTail transitions a pending row to "done" with the agent's
// returned content + truncation metadata. UPDATE semantics are
// idempotent on re-delivery of the same agent ACK.
func (r *Registry) CompleteLogTail(ctx context.Context, c LogTailCompletion) error {
	var truncatedFrom *int
	if c.Truncated {
		v := c.TruncatedFrom
		truncatedFrom = &v
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE device_log_tails
		SET status         = 'done',
		    content        = $2,
		    truncated      = $3,
		    truncated_from = $4,
		    returned_at    = $5
		WHERE correlation_id = $1
	`, c.CorrelationID, c.Content, c.Truncated, truncatedFrom, c.ReturnedAt)
	if err != nil {
		return fmt.Errorf("complete log tail: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLogTailNotFound
	}
	return nil
}

// FailLogTail transitions a pending row to "error" with the agent's
// returned error code + message. Content stays NULL.
func (r *Registry) FailLogTail(ctx context.Context, f LogTailFailure) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE device_log_tails
		SET status        = 'error',
		    error_code    = $2,
		    error_message = $3,
		    returned_at   = $4
		WHERE correlation_id = $1
	`, f.CorrelationID, f.ErrorCode, f.ErrorMessage, f.ReturnedAt)
	if err != nil {
		return fmt.Errorf("fail log tail: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLogTailNotFound
	}
	return nil
}

// DeleteStaleLogTails removes rows older than the threshold. Called
// by the cp-ingest sweeper goroutine on a ~24h cycle (per PRD).
func (r *Registry) DeleteStaleLogTails(ctx context.Context, olderThan time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM device_log_tails WHERE requested_at < $1
	`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("delete stale log tails: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *Registry) Enroll(ctx context.Context, in EnrollInput) (EnrollOutput, error) {
	if !r.cfg.BootstrapVerifier.Verify(ctx, in.BootstrapKey) {
		return EnrollOutput{}, ErrInvalidBootstrapKey
	}
	deviceID := uuid.NewString()
	cert, err := r.iot.ProvisionDevice(ctx, deviceID)
	if err != nil {
		return EnrollOutput{}, fmt.Errorf("provision iot: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO devices (
			id, hostname, hardware_uuid, hardware_kind,
			os_version, agent_version, iot_thing_arn, mtls_cert_arn,
			mtls_cert_expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		deviceID, in.Hostname, in.HardwareUUID, in.HardwareKind,
		in.OSVersion, in.AgentVersion, cert.ThingARN, cert.CertARN,
		cert.ExpiresAt,
	)
	if err != nil {
		_ = r.iot.Revoke(ctx, cert.CertARN)
		return EnrollOutput{}, fmt.Errorf("insert device: %w", err)
	}
	return EnrollOutput{
		DeviceID:          deviceID,
		MtlsCertPEM:       cert.CertPEM,
		MtlsPrivateKeyPEM: cert.PrivKeyPEM,
		IoTThingARN:       cert.ThingARN,
		MtlsCertExpiresAt: cert.ExpiresAt,
	}, nil
}

// InsertCamera adds a new camera row under deviceID with a server-
// assigned camera_id of the form camN, where N is the lowest unused
// integer at insert time. Returns ErrCameraLPRConflict if isLPR=true
// would create a second LPR row for the device (the partial unique
// index device_cameras_lpr_unique enforces single-LPR-per-device per
// ADR-030 § 1).
func (r *Registry) InsertCamera(ctx context.Context, deviceID, label, rtspURL string, isLPR bool) (cameras.Camera, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return cameras.Camera{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var maxN int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(SUBSTRING(camera_id FROM 4)::int), 0)
		FROM device_cameras
		WHERE device_id = $1 AND camera_id ~ '^cam[0-9]+$'
	`, deviceID).Scan(&maxN); err != nil {
		return cameras.Camera{}, fmt.Errorf("next camera id: %w", err)
	}
	cameraID := fmt.Sprintf("cam%d", maxN+1)

	if _, err := tx.Exec(ctx, `
		INSERT INTO device_cameras (device_id, camera_id, label, rtsp_url, is_lpr)
		VALUES ($1, $2, $3, $4, $5)
	`, deviceID, cameraID, label, rtspURL, isLPR); err != nil {
		if isLPRConflict(err) {
			return cameras.Camera{}, ErrCameraLPRConflict
		}
		return cameras.Camera{}, fmt.Errorf("insert camera: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return cameras.Camera{}, fmt.Errorf("commit: %w", err)
	}
	return cameras.Camera{CameraID: cameraID, Label: label, RtspURL: rtspURL, IsLPR: isLPR}, nil
}

// ListCameras returns the cameras for deviceID in stable insertion
// order (sorted by created_at, tiebroken on camera_id). Empty slice
// for a device with no cameras (not nil — the API hop preserves the
// distinction, but the registry's caller layer also tolerates nil).
func (r *Registry) ListCameras(ctx context.Context, deviceID string) ([]cameras.Camera, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT camera_id, label, rtsp_url, is_lpr
		FROM device_cameras
		WHERE device_id = $1
		ORDER BY created_at, camera_id
	`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list cameras: %w", err)
	}
	defer rows.Close()

	var list []cameras.Camera
	for rows.Next() {
		var c cameras.Camera
		if err := rows.Scan(&c.CameraID, &c.Label, &c.RtspURL, &c.IsLPR); err != nil {
			return nil, fmt.Errorf("scan camera: %w", err)
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cameras: %w", err)
	}
	return list, nil
}

// isLPRConflict reports whether err is a Postgres unique-violation on
// the device_cameras_lpr_unique partial index (i.e., the device
// already has another camera with is_lpr=true).
func isLPRConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "device_cameras_lpr_unique"
}

// UpdateCamera replaces label, rtsp_url, and is_lpr for the
// (deviceID, cameraID) row and returns the resulting state. Returns
// ErrCameraNotFound if no row matches and ErrCameraLPRConflict if
// flipping is_lpr=true would create a second LPR row for the
// device. updated_at is stamped server-side.
func (r *Registry) UpdateCamera(ctx context.Context, deviceID, cameraID, label, rtspURL string, isLPR bool) (cameras.Camera, error) {
	var c cameras.Camera
	err := r.pool.QueryRow(ctx, `
		UPDATE device_cameras
		SET label = $3, rtsp_url = $4, is_lpr = $5, updated_at = now()
		WHERE device_id = $1 AND camera_id = $2
		RETURNING camera_id, label, rtsp_url, is_lpr
	`, deviceID, cameraID, label, rtspURL, isLPR).Scan(&c.CameraID, &c.Label, &c.RtspURL, &c.IsLPR)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cameras.Camera{}, ErrCameraNotFound
		}
		if isLPRConflict(err) {
			return cameras.Camera{}, ErrCameraLPRConflict
		}
		return cameras.Camera{}, fmt.Errorf("update camera: %w", err)
	}
	return c, nil
}

// DeleteCamera removes the (deviceID, cameraID) row. Returns
// ErrCameraNotFound when no row matched, so callers can map to 404
// without ambiguity.
func (r *Registry) DeleteCamera(ctx context.Context, deviceID, cameraID string) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM device_cameras
		WHERE device_id = $1 AND camera_id = $2
	`, deviceID, cameraID)
	if err != nil {
		return fmt.Errorf("delete camera: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// CameraWithStatus is a camera inventory row plus its observed
// reachability status, returned by ListCamerasWithStatus for the
// device-page Cameras panel (#112, PRD #111). The plain cameras.Camera
// wire type stays status-free on purpose: it doubles as the
// cameras.update push payload to the agent (which runs
// DisallowUnknownFields), so observed status must not ride along on
// that downward config push.
type CameraWithStatus struct {
	cameras.Camera
	Status          string
	LastCheckedAt   *time.Time
	StatusChangedAt *time.Time
}

// ListCamerasWithStatus returns deviceID's cameras with their observed
// reachability status, in the same stable order as ListCameras (sorted
// by created_at, tiebroken on camera_id). The cameras API read (the
// panel) uses this; the agent push path uses the status-free
// ListCameras. Empty slice for a device with no cameras.
func (r *Registry) ListCamerasWithStatus(ctx context.Context, deviceID string) ([]CameraWithStatus, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT camera_id, label, rtsp_url, is_lpr, status, last_checked_at, status_changed_at
		FROM device_cameras
		WHERE device_id = $1
		ORDER BY created_at, camera_id
	`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list cameras with status: %w", err)
	}
	defer rows.Close()

	var list []CameraWithStatus
	for rows.Next() {
		var c CameraWithStatus
		if err := rows.Scan(&c.CameraID, &c.Label, &c.RtspURL, &c.IsLPR, &c.Status, &c.LastCheckedAt, &c.StatusChangedAt); err != nil {
			return nil, fmt.Errorf("scan camera with status: %w", err)
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cameras with status: %w", err)
	}
	return list, nil
}

// UpdateCameraStatus records one camera's probe report (#113): it sets
// status + last_checked_at to the report, and advances
// status_changed_at only when the status value actually changes. An
// idempotent re-report of the same status updates last_checked_at but
// leaves status_changed_at untouched, so it tracks the last transition,
// not the last probe. checkedAt is the report's observed time (not
// now()), so a backlogged report stamps its true time. Returns
// ErrCameraNotFound if no (deviceID, cameraID) row exists — the ingester
// treats a camera the CP doesn't know about the way the other ingesters
// treat an unknown device.
func (r *Registry) UpdateCameraStatus(ctx context.Context, deviceID, cameraID, status string, checkedAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE device_cameras
		SET last_checked_at   = $3,
		    status_changed_at = CASE WHEN status IS DISTINCT FROM $4
		                             THEN $3 ELSE status_changed_at END,
		    status            = $4,
		    updated_at        = now()
		WHERE device_id = $1 AND camera_id = $2
	`, deviceID, cameraID, checkedAt, status)
	if err != nil {
		return fmt.Errorf("update camera status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// CamerasStatus mirrors the slice-2 ServiceConfig "applied" tracking
// for the cameras.update flow: the dashboard reads LastAppliedAt +
// LastAppliedCorrelationID to render a "pending vs applied" badge on
// the Cameras panel. Both nil until the first ACK lands; after that
// CP stamps them on every successful cmd-result.
type CamerasStatus struct {
	LastAppliedAt            *time.Time
	LastAppliedCorrelationID *string
}

// GetCamerasStatus returns the (timestamp, correlation_id) pair of the
// most recent cameras.update ACK CP recorded for this device. Both
// fields are nil until the agent has ACKed at least once. Returns
// ErrDeviceNotFound if the deviceID doesn't resolve to a row.
func (r *Registry) GetCamerasStatus(ctx context.Context, deviceID string) (CamerasStatus, error) {
	if _, err := uuid.Parse(deviceID); err != nil {
		return CamerasStatus{}, ErrDeviceNotFound
	}
	var s CamerasStatus
	err := r.pool.QueryRow(ctx, `
		SELECT cameras_last_applied_at, cameras_last_applied_corr_id
		FROM devices WHERE id = $1
	`, deviceID).Scan(&s.LastAppliedAt, &s.LastAppliedCorrelationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CamerasStatus{}, ErrDeviceNotFound
		}
		return CamerasStatus{}, fmt.Errorf("get cameras status: %w", err)
	}
	return s, nil
}

// RecordCamerasApplied stamps the (timestamp, correlation_id) of a
// cameras.update ACK on the device row. Mirrors RecordServiceConfigApplied
// from slice 2: latest-wins; a non-UUID or unknown deviceID returns
// ErrDeviceNotFound so the cmd-result handler can DLQ a late ACK
// from a decommissioned device.
func (r *Registry) RecordCamerasApplied(ctx context.Context, deviceID, correlationID string, at time.Time) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE devices
		SET cameras_last_applied_at      = $2,
		    cameras_last_applied_corr_id = $3,
		    updated_at                   = now()
		WHERE id = $1
	`, deviceID, at, correlationID)
	if err != nil {
		return fmt.Errorf("record cameras applied: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// === Plate Recognizer per-device config (issue #5, ADR-030 § 3) ===

// GetPRConfig returns the CP-managed Plate Recognizer config for deviceID and
// whether a row exists (false = the device has no PR config yet — distinct from
// a zero-value config). The LPR camera URL is not stored here; callers resolve
// it from device_cameras at push time.
func (r *Registry) GetPRConfig(ctx context.Context, deviceID string) (prconfig.Config, bool, error) {
	var (
		c           prconfig.Config
		webhooksRaw []byte
		appliedAt   *time.Time
	)
	err := r.pool.QueryRow(ctx, `
		SELECT camera_id, region, enabled_webhooks,
		       last_applied_at, last_applied_corr_id
		FROM device_pr_config WHERE device_id = $1
	`, deviceID).Scan(&c.CameraID, &c.Region,
		&webhooksRaw, &appliedAt, &c.LastAppliedCorrID)
	if errors.Is(err, pgx.ErrNoRows) {
		return prconfig.Config{}, false, nil
	}
	if err != nil {
		return prconfig.Config{}, false, fmt.Errorf("get pr config: %w", err)
	}
	if len(webhooksRaw) > 0 {
		if err := json.Unmarshal(webhooksRaw, &c.Webhooks); err != nil {
			return prconfig.Config{}, false, fmt.Errorf("unmarshal webhooks: %w", err)
		}
	}
	c.LastAppliedAt = appliedAt
	return c, true, nil
}

// UpsertPRConfig inserts or replaces the editable PR config fields for deviceID
// and returns the resulting state. Used by the PUT API and by migration-time
// seeding from captured config.ini files. The last_applied_* audit fields are
// NOT touched here — they're stamped on the agent's apply-ack.
func (r *Registry) UpsertPRConfig(ctx context.Context, deviceID string, c prconfig.Config) (prconfig.Config, error) {
	webhooks := c.Webhooks
	if webhooks == nil {
		webhooks = []prconfig.Webhook{}
	}
	raw, err := json.Marshal(webhooks)
	if err != nil {
		return prconfig.Config{}, fmt.Errorf("marshal webhooks: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO device_pr_config
			(device_id, camera_id, region, enabled_webhooks)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (device_id) DO UPDATE SET
			camera_id        = EXCLUDED.camera_id,
			region           = EXCLUDED.region,
			enabled_webhooks = EXCLUDED.enabled_webhooks,
			updated_at       = now()
	`, deviceID, c.CameraID, c.Region, raw); err != nil {
		return prconfig.Config{}, fmt.Errorf("upsert pr config: %w", err)
	}
	got, _, err := r.GetPRConfig(ctx, deviceID)
	return got, err
}

// RecordPRConfigApplied stamps last_applied_at/_corr_id on the device's PR
// config row when the agent ACKs a pr.config.update (or at import/seed time,
// since the device already runs the captured config). Clears the dashboard's
// "Pending" state. No-op (not an error) if the device has no PR config row.
func (r *Registry) RecordPRConfigApplied(ctx context.Context, deviceID, correlationID string, at time.Time) error {
	if _, err := uuid.Parse(deviceID); err != nil {
		return ErrDeviceNotFound
	}
	if _, err := r.pool.Exec(ctx, `
		UPDATE device_pr_config
		SET last_applied_at = $2, last_applied_corr_id = $3, updated_at = now()
		WHERE device_id = $1
	`, deviceID, at, correlationID); err != nil {
		return fmt.Errorf("record pr config applied: %w", err)
	}
	return nil
}

// === Phase 2 Edge UI rework: network scan (issue #3) ===

// NetworkScanRequest is the create-time shape of a per-request row.
// CIDR is the operator-provided override; empty means auto-detect (the
// agent picks the device's primary subnet).
type NetworkScanRequest struct {
	CorrelationID string
	DeviceID      string
	CIDR          string // empty = auto-detect
	RequestedAt   time.Time
}

// NetworkScanCompletion is the agent's success-path payload as the
// cmd-result handler hands it to the registry.
type NetworkScanCompletion struct {
	CorrelationID string
	Hosts         []networkscan.Host
	ReturnedAt    time.Time
}

// NetworkScanFailure is the agent's error-path payload. ErrorCode is a
// stable string (e.g. "network_scan.scan_failed") the dashboard can
// surface; ErrorMessage is the human-readable elaboration.
type NetworkScanFailure struct {
	CorrelationID string
	ErrorCode     string
	ErrorMessage  string
	ReturnedAt    time.Time
}

// NetworkScan is the row shape returned by GetNetworkScan. CIDR is *string
// rather than string so the dashboard can distinguish "auto-detect was
// requested" (nil) from "operator typed in 10.0.0.0/24" (non-nil).
// Result is *networkscan.Response so a pending row is nil-result; a
// completed row carries the agent's hosts list.
type NetworkScan struct {
	CorrelationID string
	DeviceID      string
	CIDR          *string
	Status        string // pending | done | error
	Result        *networkscan.Response
	ErrorCode     *string
	ErrorMessage  *string
	RequestedAt   time.Time
	ReturnedAt    *time.Time
}

// CreateNetworkScanRequest persists a fresh "pending" row keyed by
// correlation_id. The CP API publishes the network.scan cmd immediately
// after; the agent's ACK lands as a Complete or Fail call from the
// cmd-result handler.
func (r *Registry) CreateNetworkScanRequest(ctx context.Context, req NetworkScanRequest) error {
	var cidr *string
	if req.CIDR != "" {
		v := req.CIDR
		cidr = &v
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO device_network_scans (
			correlation_id, device_id, cidr_requested, status, requested_at
		) VALUES ($1, $2, $3, 'pending', $4)
	`, req.CorrelationID, req.DeviceID, cidr, req.RequestedAt); err != nil {
		return fmt.Errorf("insert device_network_scans: %w", err)
	}
	return nil
}

// GetNetworkScan returns one row by correlation_id, or ErrNetworkScanNotFound.
func (r *Registry) GetNetworkScan(ctx context.Context, correlationID string) (NetworkScan, error) {
	var (
		n         NetworkScan
		resultRaw []byte
	)
	err := r.pool.QueryRow(ctx, `
		SELECT correlation_id, device_id, cidr_requested,
		       status, result, error_code, error_message,
		       requested_at, returned_at
		FROM device_network_scans
		WHERE correlation_id = $1
	`, correlationID).Scan(
		&n.CorrelationID, &n.DeviceID, &n.CIDR,
		&n.Status, &resultRaw, &n.ErrorCode, &n.ErrorMessage,
		&n.RequestedAt, &n.ReturnedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return NetworkScan{}, ErrNetworkScanNotFound
		}
		return NetworkScan{}, fmt.Errorf("get network scan: %w", err)
	}
	if len(resultRaw) > 0 {
		var resp networkscan.Response
		if err := json.Unmarshal(resultRaw, &resp); err != nil {
			return NetworkScan{}, fmt.Errorf("decode network scan result: %w", err)
		}
		n.Result = &resp
	}
	return n, nil
}

// CompleteNetworkScan transitions a pending row to "done" with the
// agent's returned hosts list. Re-delivery of the same agent ACK is
// idempotent via UPDATE semantics.
func (r *Registry) CompleteNetworkScan(ctx context.Context, c NetworkScanCompletion) error {
	hosts := c.Hosts
	if hosts == nil {
		hosts = []networkscan.Host{}
	}
	resultBytes, err := json.Marshal(networkscan.Response{Hosts: hosts})
	if err != nil {
		return fmt.Errorf("encode network scan result: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE device_network_scans
		SET status      = 'done',
		    result      = $2,
		    returned_at = $3
		WHERE correlation_id = $1
	`, c.CorrelationID, resultBytes, c.ReturnedAt)
	if err != nil {
		return fmt.Errorf("complete network scan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNetworkScanNotFound
	}
	return nil
}

// FailNetworkScan transitions a pending row to "error" with the agent's
// returned error code + message. Result stays NULL.
func (r *Registry) FailNetworkScan(ctx context.Context, f NetworkScanFailure) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE device_network_scans
		SET status        = 'error',
		    error_code    = $2,
		    error_message = $3,
		    returned_at   = $4
		WHERE correlation_id = $1
	`, f.CorrelationID, f.ErrorCode, f.ErrorMessage, f.ReturnedAt)
	if err != nil {
		return fmt.Errorf("fail network scan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNetworkScanNotFound
	}
	return nil
}

// DeleteStaleNetworkScans removes rows older than the threshold. Called
// by the cp-ingest sweeper goroutine on a ~24h cycle (same pattern as
// DeleteStaleLogTails).
func (r *Registry) DeleteStaleNetworkScans(ctx context.Context, olderThan time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM device_network_scans WHERE requested_at < $1
	`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("delete stale network scans: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
