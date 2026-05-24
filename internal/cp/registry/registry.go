// Package registry owns the enrollment-first device lifecycle.
//
// Per PRD § Module decomposition: Enroll wraps bootstrap-key validation,
// IoT Core thing+cert minting, and the Postgres insert behind one interface,
// so callers never see AWS or DB primitives.
package registry

import (
	"context"
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
		       s.name AS site_name, c.name AS client_name
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
		       s.name AS site_name, c.name AS client_name
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
