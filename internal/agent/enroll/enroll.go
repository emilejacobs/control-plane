// Package enroll is the device side of Enrollment: it presents the bundled
// bootstrap key to cp-api's POST /enrollments, installs the returned mTLS
// cert/key + the bundled AWS IoT CA, and writes a loadable agent-config.
//
// It is the Go replacement for the curl-and-jq enrollment step that lived in
// the mac-mini-rollout install module (ADR-037 — install folded into the agent
// binary). Enrollment is idempotent on the CP side via the hardware-UUID
// Idempotency-Key, so a re-run overwrites the same files with the same content
// rather than registering a duplicate device (ADR-036).
package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/emilejacobs/control-plane/internal/config"
)

// ErrInvalidBootstrapKey is returned when cp-api rejects the bootstrap key
// (HTTP 401). It is distinguished so the installer can surface a clear "wrong
// or revoked bootstrap key" message rather than a generic enrollment failure.
var ErrInvalidBootstrapKey = errors.New("enroll: invalid bootstrap key")

// Hardware is the device identity the enrollment request carries. The install
// step gathers these from the host (ioreg UUID, hostname, sw_vers, the
// build-stamped agent version).
type Hardware struct {
	Hostname     string
	HardwareUUID string
	HardwareKind string // "mac"
	OSVersion    string
	AgentVersion string
}

// Params are the inputs to Enroll.
type Params struct {
	// CPBaseURL is cp-api's public base, e.g. https://api.control.uknomi.com.
	CPBaseURL string
	// BootstrapKey is the static key bundled in the install pkg (ADR-017).
	BootstrapKey string
	Hardware     Hardware
	// CACertPEM is the AWS IoT root CA bundled with the install package.
	CACertPEM []byte
	// RuntimeDir is where cert.pem / key.pem / ca.pem / agent-config.json land
	// (e.g. /var/uknomi). It must already exist.
	RuntimeDir string
	// BrokerURL is the MQTT endpoint written into the agent-config, e.g.
	// tls://<ats-endpoint>:8883. Fleet-static, supplied by the installer.
	BrokerURL string
	// Defaults seeds the operational fields of the written agent-config
	// (intervals, service allow-list, cameras/snapshot paths, auto-login user).
	// Enroll fills in the identity + cert fields from the response.
	Defaults config.Config
	// HTTPClient is optional; nil uses a client with a 30s timeout.
	HTTPClient *http.Client
}

// Result reports what Enroll produced.
type Result struct {
	DeviceID   string
	ConfigPath string
	CertPath   string
}

type enrollRequest struct {
	BootstrapKey string `json:"bootstrap_key"`
	Hostname     string `json:"hostname"`
	HardwareUUID string `json:"hardware_uuid"`
	HardwareKind string `json:"hardware_kind"`
	OSVersion    string `json:"os_version"`
	AgentVersion string `json:"agent_version"`
}

type enrollResponse struct {
	DeviceID          string `json:"device_id"`
	MtlsCertPEM       string `json:"mtls_cert_pem"`
	MtlsPrivateKeyPEM string `json:"mtls_private_key_pem"`
	IoTEndpoint       string `json:"iot_endpoint"`
	IoTThingARN       string `json:"iot_thing_arn"`
	MtlsCertExpiresAt string `json:"mtls_cert_expires_at"`
}

// Enroll registers the device with the CP and provisions its mTLS identity.
func Enroll(ctx context.Context, p Params) (Result, error) {
	resp, err := postEnrollment(ctx, p)
	if err != nil {
		return Result{}, err
	}

	certPath := filepath.Join(p.RuntimeDir, "cert.pem")
	keyPath := filepath.Join(p.RuntimeDir, "key.pem")
	caPath := filepath.Join(p.RuntimeDir, "ca.pem")
	cfgPath := filepath.Join(p.RuntimeDir, "agent-config.json")

	// Private material is owner-only; the CA is world-readable (it is public).
	if err := os.WriteFile(certPath, []byte(resp.MtlsCertPEM), 0o600); err != nil {
		return Result{}, fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(resp.MtlsPrivateKeyPEM), 0o600); err != nil {
		return Result{}, fmt.Errorf("write key: %w", err)
	}
	if err := os.WriteFile(caPath, p.CACertPEM, 0o644); err != nil {
		return Result{}, fmt.Errorf("write ca: %w", err)
	}

	cfg := p.Defaults
	cfg.DeviceID = resp.DeviceID
	cfg.ClientID = resp.DeviceID
	cfg.Version = p.Hardware.AgentVersion
	cfg.BrokerURL = p.BrokerURL
	cfg.CertPath = certPath
	cfg.KeyPath = keyPath
	cfg.CACertPath = caPath

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal agent-config: %w", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return Result{}, fmt.Errorf("write agent-config: %w", err)
	}

	return Result{DeviceID: resp.DeviceID, ConfigPath: cfgPath, CertPath: certPath}, nil
}

func postEnrollment(ctx context.Context, p Params) (enrollResponse, error) {
	body, err := json.Marshal(enrollRequest{
		BootstrapKey: p.BootstrapKey,
		Hostname:     p.Hardware.Hostname,
		HardwareUUID: p.Hardware.HardwareUUID,
		HardwareKind: p.Hardware.HardwareKind,
		OSVersion:    p.Hardware.OSVersion,
		AgentVersion: p.Hardware.AgentVersion,
	})
	if err != nil {
		return enrollResponse{}, fmt.Errorf("marshal enrollment request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.CPBaseURL+"/enrollments", bytes.NewReader(body))
	if err != nil {
		return enrollResponse{}, fmt.Errorf("build enrollment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The hardware UUID is the idempotency key: a retry (or a re-run of the
	// installer) returns the original enrollment rather than a duplicate.
	req.Header.Set("Idempotency-Key", p.Hardware.HardwareUUID)

	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	httpResp, err := client.Do(req)
	if err != nil {
		return enrollResponse{}, fmt.Errorf("post enrollment: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode == http.StatusUnauthorized {
		return enrollResponse{}, ErrInvalidBootstrapKey
	}
	if httpResp.StatusCode != http.StatusCreated {
		return enrollResponse{}, fmt.Errorf("enrollment failed: HTTP %d", httpResp.StatusCode)
	}

	var resp enrollResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return enrollResponse{}, fmt.Errorf("decode enrollment response: %w", err)
	}
	return resp, nil
}
