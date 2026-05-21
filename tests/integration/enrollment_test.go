package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

func TestEnrollmentHappyPath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	iot := iotprovisioner.NewFake()
	reg := registry.New(pool, iot, registry.Config{BootstrapKey: "test-bootstrap-key"})
	srv := httptest.NewServer(api.NewRouter(api.Deps{Registry: reg}))
	t.Cleanup(srv.Close)

	body := map[string]any{
		"bootstrap_key": "test-bootstrap-key",
		"hostname":      "mac-mini-acme-01",
		"hardware_uuid": "11111111-2222-3333-4444-555555555555",
		"hardware_kind": "mac",
		"os_version":    "macOS 15.0",
		"agent_version": "0.1.0",
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequest("POST", srv.URL+"/enrollments", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "11111111-2222-3333-4444-555555555555")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 201; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		DeviceID           string `json:"device_id"`
		MtlsCertPEM        string `json:"mtls_cert_pem"`
		MtlsPrivateKeyPEM  string `json:"mtls_private_key_pem"`
		IoTEndpoint        string `json:"iot_endpoint"`
		IoTThingARN        string `json:"iot_thing_arn"`
		MtlsCertExpiresAt  string `json:"mtls_cert_expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.DeviceID == "" {
		t.Errorf("device_id is empty")
	}
	if out.MtlsCertPEM == "" {
		t.Errorf("mtls_cert_pem is empty")
	}
	if out.MtlsPrivateKeyPEM == "" {
		t.Errorf("mtls_private_key_pem is empty")
	}
	if out.IoTThingARN == "" {
		t.Errorf("iot_thing_arn is empty")
	}
	if out.MtlsCertExpiresAt == "" {
		t.Errorf("mtls_cert_expires_at is empty")
	}

	var hostname, hwUUID string
	err = pool.QueryRow(ctx,
		`SELECT hostname, hardware_uuid FROM devices WHERE id = $1`,
		out.DeviceID,
	).Scan(&hostname, &hwUUID)
	if err != nil {
		t.Fatalf("query devices row: %v", err)
	}
	if hostname != "mac-mini-acme-01" {
		t.Errorf("hostname: got %q want %q", hostname, "mac-mini-acme-01")
	}
	if hwUUID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("hardware_uuid: got %q want %q", hwUUID, "11111111-2222-3333-4444-555555555555")
	}
}
