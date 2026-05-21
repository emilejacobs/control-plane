// Package iotprovisioner wraps AWS IoT Core thing+cert provisioning behind
// a Provisioner interface, so the rest of the CP never sees AWS primitives.
//
// Per PRD § Module decomposition: the Fake satisfies unit tests; the AWS-SDK
// implementation lands when wiring cmd/cp-api against a real AWS account.
package iotprovisioner

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DeviceCert is the per-device material returned to the install script.
// CertPEM and PrivKeyPEM are returned once at enrollment and never persisted
// by the CP; CertARN and ThingARN are persisted on the devices row.
type DeviceCert struct {
	ThingARN   string
	CertARN    string
	CertPEM    string
	PrivKeyPEM string
	ExpiresAt  time.Time
}

type Provisioner interface {
	ProvisionDevice(ctx context.Context, deviceID string) (DeviceCert, error)
	Revoke(ctx context.Context, certARN string) error
}

// Fake is a deterministic in-process Provisioner for tests. It does not talk
// to AWS and produces predictable fake PEM blobs keyed by call sequence.
type Fake struct {
	mu  sync.Mutex
	seq int
}

func NewFake() *Fake { return &Fake{} }

func (f *Fake) ProvisionDevice(_ context.Context, deviceID string) (DeviceCert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return DeviceCert{
		ThingARN:   fmt.Sprintf("arn:aws:iot:test:thing/%s", deviceID),
		CertARN:    fmt.Sprintf("arn:aws:iot:test:cert/%d", f.seq),
		CertPEM:    fmt.Sprintf("-----BEGIN CERTIFICATE-----\nfake-cert-%d\n-----END CERTIFICATE-----\n", f.seq),
		PrivKeyPEM: fmt.Sprintf("-----BEGIN PRIVATE KEY-----\nfake-key-%d\n-----END PRIVATE KEY-----\n", f.seq),
		ExpiresAt:  time.Now().Add(365 * 24 * time.Hour),
	}, nil
}

func (f *Fake) Revoke(_ context.Context, _ string) error { return nil }

// Count returns the number of successful ProvisionDevice calls. Test-only.
func (f *Fake) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seq
}
