package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// postEnroll sends a POST /enrollments request and returns (status, raw body).
// Lower-level than enrollForTest because idempotency replay tests need to
// inspect status codes (201 vs 200) and compare raw bytes across calls.
func postEnroll(t *testing.T, srv *testServer, body []byte, idempotencyKey string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/enrollments", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, raw
}

func TestEnrollmentIdempotentReplay(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	hwUUID := "33333333-3333-3333-3333-333333333333"
	body, err := json.Marshal(map[string]any{
		"bootstrap_key": testBootstrapKey,
		"hostname":      "mac-mini-acme-03",
		"hardware_uuid": hwUUID,
		"hardware_kind": "mac",
		"os_version":    "macOS 15.0",
		"agent_version": "0.1.0",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	firstStatus, firstBody := postEnroll(t, srv, body, hwUUID)
	if firstStatus != http.StatusCreated {
		t.Fatalf("first status: got %d want 201; body=%s", firstStatus, firstBody)
	}

	secondStatus, secondBody := postEnroll(t, srv, body, hwUUID)
	if secondStatus != http.StatusOK {
		t.Fatalf("replay status: got %d want 200; body=%s", secondStatus, secondBody)
	}
	if !bytes.Equal(firstBody, secondBody) {
		t.Errorf("replay body differs from first.\n  first:  %s\n  second: %s", firstBody, secondBody)
	}

	var rowCount int
	if err := srv.Pool.QueryRow(ctx,
		`SELECT count(*) FROM devices WHERE hardware_uuid = $1`, hwUUID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("devices row count: got %d want 1", rowCount)
	}

	if calls := srv.IoT.Count(); calls != 1 {
		t.Errorf("IoT ProvisionDevice calls: got %d want 1 (replay must not mint a new cert)", calls)
	}
}
