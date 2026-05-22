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

	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
)

// ErrInvalidBootstrapKey is returned by Enroll when the supplied bootstrap
// key does not match the one configured on the Registry. Handlers translate
// it to HTTP 401 (per PRD § API contracts).
var ErrInvalidBootstrapKey = errors.New("invalid bootstrap key")

// ErrDeviceNotFound is returned by GetByID when no row matches the id.
// Handlers translate it to HTTP 404.
var ErrDeviceNotFound = errors.New("device not found")

type Config struct {
	BootstrapKey string
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
}

func (r *Registry) GetByID(ctx context.Context, id string) (Device, error) {
	var d Device
	err := r.pool.QueryRow(ctx, `
		SELECT id, hostname, hardware_uuid, hardware_kind,
		       os_version, agent_version, iot_thing_arn,
		       last_seen, is_online, presence_changed_at, enrolled_at
		FROM devices WHERE id = $1
	`, id).Scan(
		&d.ID, &d.Hostname, &d.HardwareUUID, &d.HardwareKind,
		&d.OSVersion, &d.AgentVersion, &d.IoTThingARN,
		&d.LastSeen, &d.IsOnline, &d.PresenceChangedAt, &d.EnrolledAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Device{}, ErrDeviceNotFound
		}
		return Device{}, fmt.Errorf("get device: %w", err)
	}
	return d, nil
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
	if in.BootstrapKey != r.cfg.BootstrapKey {
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
			os_version, agent_version, iot_thing_arn, mtls_cert_arn
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		deviceID, in.Hostname, in.HardwareUUID, in.HardwareKind,
		in.OSVersion, in.AgentVersion, cert.ThingARN, cert.CertARN,
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
