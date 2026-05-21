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

	resp, err := http.Get(srv.URL + "/devices/" + deviceID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
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

func TestGetDeviceByIDUnknownReturns404(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// A syntactically-valid UUID that won't match any row.
	resp, err := http.Get(srv.URL + "/devices/00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 404; body=%s", resp.StatusCode, raw)
	}
}
