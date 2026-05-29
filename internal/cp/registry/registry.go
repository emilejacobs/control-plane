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
		       devices.lan_ip, devices.tailscale_ip, devices.tailscale_name
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
		       s.name AS site_name, c.name AS client_name,
		       devices.asset_number,
		       devices.lan_ip, devices.tailscale_ip, devices.tailscale_name
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
			&d.SiteName, &d.ClientName,
			&d.AssetNumber,
			&d.LanIP, &d.TailscaleIP, &d.TailscaleName,
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
