package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestEnrollmentRejectsUnknownBootstrapKey(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	hwUUID := "55555555-5555-5555-5555-555555555555"
	body, err := json.Marshal(map[string]any{
		"bootstrap_key": "wrong-key",
		"hostname":      "mac-mini-acme-05",
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

	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 401; body=%s", resp.StatusCode, raw)
	}

	// No row, no IoT call — the request must have been rejected before
	// the provisioner ran.
	var rowCount int
	if err := srv.Pool.QueryRow(ctx, `SELECT count(*) FROM devices`).Scan(&rowCount); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("devices row count: got %d want 0", rowCount)
	}
	if calls := srv.IoT.Count(); calls != 0 {
		t.Errorf("IoT ProvisionDevice calls: got %d want 0", calls)
	}

	// 4xx must not be cached by the idempotency middleware: a retry with the
	// correct bootstrap key under the same Idempotency-Key must still succeed.
	good, err := json.Marshal(map[string]any{
		"bootstrap_key": testBootstrapKey,
		"hostname":      "mac-mini-acme-05",
		"hardware_uuid": hwUUID,
		"hardware_kind": "mac",
		"os_version":    "macOS 15.0",
		"agent_version": "0.1.0",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	retry, err := http.NewRequest(http.MethodPost, srv.URL+"/enrollments", bytes.NewReader(good))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	retry.Header.Set("Content-Type", "application/json")
	retry.Header.Set("Idempotency-Key", hwUUID)
	resp2, err := http.DefaultClient.Do(retry)
	if err != nil {
		t.Fatalf("retry do: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp2.Body)
		t.Fatalf("retry status: got %d want 201; body=%s", resp2.StatusCode, raw)
	}
}
