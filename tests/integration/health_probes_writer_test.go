package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// TestRegistryRecordHealthProbesHappyPath — Phase 2 fleet-health-probes
// (#19) storage cycle: RecordHealthProbes persists per-probe rows for an
// enrolled device, and a follow-up SELECT round-trips status, state, the
// jsonb details payload, and last_observed_at.
func TestRegistryRecordHealthProbesHappyPath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-probes-01", "11111111-2222-3333-4444-555555555555")

	observedAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	results := []healthprobes.Result{
		{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
		{
			Name:    healthprobes.ProbeWhisperModel,
			Status:  healthprobes.StatusGreen,
			State:   "present",
			Details: map[string]any{"variant": "medium.en", "size_mb": 539},
		},
	}
	if err := srv.Registry.RecordHealthProbes(ctx, deviceID, results, observedAt); err != nil {
		t.Fatalf("RecordHealthProbes: %v", err)
	}

	var (
		gotStatus  string
		gotState   string
		gotDetails []byte
		gotAt      time.Time
	)
	if err := srv.Pool.QueryRow(ctx, `
		SELECT status, state, details, last_observed_at
		FROM device_health_probes
		WHERE device_id = $1 AND probe_name = $2
	`, deviceID, healthprobes.ProbeWhisperModel).Scan(&gotStatus, &gotState, &gotDetails, &gotAt); err != nil {
		t.Fatalf("select whisper probe row: %v", err)
	}
	if gotStatus != "green" || gotState != "present" {
		t.Errorf("status/state = %q/%q, want green/present", gotStatus, gotState)
	}
	if !gotAt.Equal(observedAt) {
		t.Errorf("last_observed_at = %v, want %v", gotAt, observedAt)
	}
	var details map[string]any
	if err := json.Unmarshal(gotDetails, &details); err != nil {
		t.Fatalf("details not valid jsonb: %v", err)
	}
	if details["variant"] != "medium.en" {
		t.Errorf("details[variant] = %v, want medium.en", details["variant"])
	}
}

// TestRegistryRecordHealthProbesUpsertIdempotent — re-reporting the same
// probe overwrites the prior row rather than duplicating it (PK
// device_id, probe_name), so the table always reflects latest-observed.
func TestRegistryRecordHealthProbesUpsertIdempotent(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-probes-02", "22222222-3333-4444-5555-666666666666")

	t0 := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	if err := srv.Registry.RecordHealthProbes(ctx, deviceID, []healthprobes.Result{
		{Name: healthprobes.ProbeGUISession, Status: healthprobes.StatusGreen, State: "active"},
	}, t0); err != nil {
		t.Fatalf("first RecordHealthProbes: %v", err)
	}
	t1 := t0.Add(5 * time.Minute)
	if err := srv.Registry.RecordHealthProbes(ctx, deviceID, []healthprobes.Result{
		{Name: healthprobes.ProbeGUISession, Status: healthprobes.StatusRed, State: "login_window"},
	}, t1); err != nil {
		t.Fatalf("second RecordHealthProbes: %v", err)
	}

	var count int
	if err := srv.Pool.QueryRow(ctx,
		`SELECT count(*) FROM device_health_probes WHERE device_id = $1 AND probe_name = $2`,
		deviceID, healthprobes.ProbeGUISession).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1 (UPSERT, not duplicate)", count)
	}
	var status, state string
	_ = srv.Pool.QueryRow(ctx,
		`SELECT status, state FROM device_health_probes WHERE device_id = $1 AND probe_name = $2`,
		deviceID, healthprobes.ProbeGUISession).Scan(&status, &state)
	if status != "red" || state != "login_window" {
		t.Errorf("after upsert status/state = %q/%q, want red/login_window", status, state)
	}
}

// TestRegistryRecordHealthProbesUnknownDevice — a report for a device id
// that resolves to no row (including a non-UUID) returns ErrDeviceNotFound
// so the ingester can DLQ a late report rather than loop.
func TestRegistryRecordHealthProbesUnknownDevice(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	err := srv.Registry.RecordHealthProbes(ctx, "99999999-0000-0000-0000-000000000000",
		[]healthprobes.Result{{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusRed, State: "missing"}},
		time.Now())
	if !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Fatalf("err = %v, want ErrDeviceNotFound", err)
	}
}

// TestGetDeviceHealthProbesEndpoint — the GET /devices/{id}/health-probes
// API round-trips stored probes through the real router + authz + DB.
func TestGetDeviceHealthProbesEndpoint(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-probes-api", "44444444-5555-6666-7777-888888888888")

	observedAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	if err := srv.Registry.RecordHealthProbes(ctx, deviceID, []healthprobes.Result{
		{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
		{
			Name:    healthprobes.ProbeWhisperModel,
			Status:  healthprobes.StatusGreen,
			State:   "present",
			Details: map[string]any{"variant": "medium.en", "size_mb": 539},
		},
	}, observedAt); err != nil {
		t.Fatalf("RecordHealthProbes: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/devices/"+deviceID+"/health-probes", nil)
	req.Header.Set("Authorization", "Bearer "+mintAccessToken(t, ctx, srv))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET health-probes: status %d; body=%s", resp.StatusCode, raw)
	}

	var body struct {
		Probes []struct {
			Name           string         `json:"name"`
			Status         string         `json:"status"`
			State          string         `json:"state"`
			Details        map[string]any `json:"details"`
			LastObservedAt string         `json:"last_observed_at"`
		} `json:"probes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Probes) != 2 {
		t.Fatalf("probes len = %d, want 2 (ordered by probe_name)", len(body.Probes))
	}
	// Ordered by probe_name: auto_login before whisper_model.
	if body.Probes[0].Name != healthprobes.ProbeAutoLogin || body.Probes[0].State != "configured" {
		t.Errorf("probe[0] = %+v", body.Probes[0])
	}
	if body.Probes[1].Name != healthprobes.ProbeWhisperModel {
		t.Errorf("probe[1].Name = %q, want whisper_model", body.Probes[1].Name)
	}
	if v, _ := body.Probes[1].Details["variant"].(string); v != "medium.en" {
		t.Errorf("whisper details variant = %v, want medium.en", body.Probes[1].Details["variant"])
	}
	if body.Probes[0].LastObservedAt != observedAt.Format(time.RFC3339) {
		t.Errorf("last_observed_at = %q, want %q", body.Probes[0].LastObservedAt, observedAt.Format(time.RFC3339))
	}
}
