package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestCorrelationIDFlowsEndToEnd is the in-process half of Issue 19
// acceptance criterion 3: a request with X-Correlation-Id reaches the
// access-log line carrying the same id. The cross-service half (cp-ingest
// + audit_log) lands with #07 / #20 once those exist.
func TestCorrelationIDFlowsEndToEnd(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	const corrID = "corr-end-to-end-99"
	const hwUUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	body, err := json.Marshal(map[string]any{
		"bootstrap_key": testBootstrapKey,
		"hostname":      "mac-mini-corr-test",
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
	req.Header.Set("X-Correlation-Id", corrID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 201; body=%s", resp.StatusCode, raw)
	}

	if got := resp.Header.Get("X-Correlation-Id"); got != corrID {
		t.Errorf("response header: got %q want %q", got, corrID)
	}

	// The cplog access-log line should be in the captured buffer, tagged
	// with the inbound correlation_id.
	found := false
	for line := range strings.SplitSeq(srv.Logs.String(), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["msg"] == "request completed" &&
			entry["correlation_id"] == corrID &&
			entry["path"] == "/enrollments" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no `request completed` log line found tagged with correlation_id=%q.\nFull log buffer:\n%s",
			corrID, srv.Logs.String())
	}
}
