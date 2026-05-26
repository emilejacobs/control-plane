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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

// ErrInvalidBootstrapKey is returned by Enroll when the supplied bootstrap
// key is rejected by the verifier. Handlers translate it to HTTP 401 (per
// PRD § API contracts).
var ErrInvalidBootstrapKey = errors.New("invalid bootstrap key")

// ErrDeviceNotFound is returned by GetByID when no row matches the id.
// Handlers translate it to HTTP 404.
var ErrDeviceNotFound = errors.New("device not found")

// ErrLogTailNotFound is returned by GetLogTail when no row matches the
// correlation_id. Handlers translate it to HTTP 404.
var ErrLogTailNotFound = errors.New("log tail not found")

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
	// SiteName and ClientName are resolved by GetByID and List via the site
	// model; nil for a device with no site assigned.
	SiteName   *string
	ClientName *string
	// AssetNumber is the fleet-tracking identifier set during install
	// (migration 014). Nil until install-module 11 starts shipping it.
	AssetNumber *string
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
		       s.name AS site_name, c.name AS client_name,
		       devices.asset_number
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
		&d.SiteName, &d.ClientName,
		&d.AssetNumber,
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
		       devices.asset_number
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
