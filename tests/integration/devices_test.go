package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// enrollForTest is a thin helper around POST /enrollments used by tests that
// need an already-enrolled device. It panics-via-t.Fatal on any failure so
// that the assertion site stays focused on the behavior under test.
func enrollForTest(t *testing.T, srv *testServer, hostname, hwUUID string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"bootstrap_key": testBootstrapKey,
		"hostname":      hostname,
		"hardware_uuid": hwUUID,
		"hardware_kind": "mac",
		"os_version":    "macOS 15.0",
		"agent_version": "0.1.0",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/enrollments", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", hwUUID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("enroll: status %d; body=%s", resp.StatusCode, raw)
	}
	var out struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.DeviceID
}

func TestGetDeviceByIDReturnsInsertedRow(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-acme-02", "22222222-2222-3333-4444-555555555555")

	resp := doDeviceGet(t, srv.URL, deviceID, mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		DeviceID     string `json:"device_id"`
		Hostname     string `json:"hostname"`
		HardwareUUID string `json:"hardware_uuid"`
		HardwareKind string `json:"hardware_kind"`
		OSVersion    string `json:"os_version"`
		AgentVersion string `json:"agent_version"`
		IoTThingARN  string `json:"iot_thing_arn"`
		EnrolledAt   string `json:"enrolled_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.DeviceID != deviceID {
		t.Errorf("device_id: got %q want %q", out.DeviceID, deviceID)
	}
	if out.Hostname != "mac-mini-acme-02" {
		t.Errorf("hostname: got %q want %q", out.Hostname, "mac-mini-acme-02")
	}
	if out.HardwareUUID != "22222222-2222-3333-4444-555555555555" {
		t.Errorf("hardware_uuid: got %q", out.HardwareUUID)
	}
	if out.HardwareKind != "mac" {
		t.Errorf("hardware_kind: got %q want %q", out.HardwareKind, "mac")
	}
	if out.OSVersion != "macOS 15.0" {
		t.Errorf("os_version: got %q", out.OSVersion)
	}
	if out.AgentVersion != "0.1.0" {
		t.Errorf("agent_version: got %q", out.AgentVersion)
	}
	if out.IoTThingARN == "" {
		t.Errorf("iot_thing_arn is empty")
	}
	// enrolled_at should be an RFC3339 timestamp from the last few seconds.
	enrolledAt, err := time.Parse(time.RFC3339, out.EnrolledAt)
	if err != nil {
		t.Errorf("enrolled_at not RFC3339: %v", err)
	} else if time.Since(enrolledAt) > 30*time.Second {
		t.Errorf("enrolled_at too old: %v", enrolledAt)
	}
}

func TestGetDeviceByIDSurfacesCertExpiry(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-acme-09", "09090909-0909-4909-8909-090909090909")

	resp := doDeviceGet(t, srv.URL, deviceID, mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		MtlsCertExpiresAt     *string `json:"mtls_cert_expires_at"`
		MtlsCertDaysRemaining *int    `json:"mtls_cert_days_remaining"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The cert expiry minted at enrollment must round-trip through the
	// devices row and back out on the read endpoint.
	if out.MtlsCertExpiresAt == nil {
		t.Fatalf("mtls_cert_expires_at is null — not persisted at enrollment")
	}
	expiresAt, err := time.Parse(time.RFC3339, *out.MtlsCertExpiresAt)
	if err != nil {
		t.Fatalf("mtls_cert_expires_at not RFC3339: %v", err)
	}
	// The fake provisioner mints a 365-day cert.
	if until := time.Until(expiresAt); until < 360*24*time.Hour || until > 366*24*time.Hour {
		t.Errorf("mtls_cert_expires_at %v not ~365d out (%v remaining)", expiresAt, until)
	}
	if out.MtlsCertDaysRemaining == nil {
		t.Fatalf("mtls_cert_days_remaining is null")
	}
	if d := *out.MtlsCertDaysRemaining; d < 363 || d > 365 {
		t.Errorf("mtls_cert_days_remaining: got %d want ~365", d)
	}
}

func TestGetDeviceByIDUnknownReturns404(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// A syntactically-valid UUID that won't match any row.
	resp := doDeviceGet(t, srv.URL, "00000000-0000-0000-0000-000000000000", mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 404; body=%s", resp.StatusCode, raw)
	}
}

// doDeviceGet issues an authenticated GET /devices/{id}. The caller owns
// resp.Body.
func doDeviceGet(t *testing.T, baseURL, deviceID, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/devices/"+deviceID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}
