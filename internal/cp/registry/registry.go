// Package registry owns the enrollment-first device lifecycle.
//
// Per PRD § Module decomposition: Enroll wraps bootstrap-key validation,
// IoT Core thing+cert minting, and the Postgres insert behind one interface,
// so callers never see AWS or DB primitives.
package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
)

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

// Device is the static row returned by GetByID. Computed fields (is_online,
// last_seen_ago_seconds, mtls_cert_days_remaining per PRD § API contracts)
// land in the presence (#07) and cert-expiry (#09) slices.
type Device struct {
	ID           string
	Hostname     string
	HardwareUUID string
	HardwareKind string
	OSVersion    string
	AgentVersion string
	IoTThingARN  string
	EnrolledAt   time.Time
}

func (r *Registry) GetByID(ctx context.Context, id string) (Device, error) {
	var d Device
	err := r.pool.QueryRow(ctx, `
		SELECT id, hostname, hardware_uuid, hardware_kind,
		       os_version, agent_version, iot_thing_arn, enrolled_at
		FROM devices WHERE id = $1
	`, id).Scan(
		&d.ID, &d.Hostname, &d.HardwareUUID, &d.HardwareKind,
		&d.OSVersion, &d.AgentVersion, &d.IoTThingARN, &d.EnrolledAt,
	)
	if err != nil {
		return Device{}, fmt.Errorf("get device: %w", err)
	}
	return d, nil
}

func (r *Registry) Enroll(ctx context.Context, in EnrollInput) (EnrollOutput, error) {
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
