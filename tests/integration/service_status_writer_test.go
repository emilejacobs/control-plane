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
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

// TestRegistryRecordServiceStatesHappyPath — Phase 2 service-status
// slice 1, storage cycle: RecordServiceStates persists per-service
// rows for an enrolled device, and a follow-up SELECT round-trips
// every field the dashboard will render (state, state_since,
// last_reported, by-device + service_name PK).
func TestRegistryRecordServiceStatesHappyPath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-svcstatus-01", "66666666-6666-7777-7777-888888888888")

	reportedAt := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	edgeSince := reportedAt.Add(-2 * time.Hour)
	nginxSince := reportedAt.Add(-30 * time.Second)

	states := []servicestatus.ServiceState{
		{Name: "com.uknomi.edge-ui", State: service.StateRunning, StateSince: edgeSince},
		{Name: "nginx", State: service.StateStopped, StateSince: nginxSince},
	}
	if err := srv.Registry.RecordServiceStates(ctx, deviceID, states, reportedAt); err != nil {
		t.Fatalf("RecordServiceStates: %v", err)
	}

	rows, err := srv.Pool.Query(ctx, `
		SELECT service_name, state, state_since, last_reported
		FROM device_services
		WHERE device_id = $1
		ORDER BY service_name
	`, deviceID)
	if err != nil {
		t.Fatalf("query device_services: %v", err)
	}
	defer rows.Close()

	type row struct {
		Name         string
		State        string
		StateSince   time.Time
		LastReported time.Time
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Name, &r.State, &r.StateSince, &r.LastReported); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("rows: got %d, want 2", len(got))
	}
	if got[0].Name != "com.uknomi.edge-ui" || got[0].State != "running" || !got[0].StateSince.Equal(edgeSince) || !got[0].LastReported.Equal(reportedAt) {
		t.Errorf("edge-ui row: got %+v, want name=com.uknomi.edge-ui state=running state_since=%v last_reported=%v",
			got[0], edgeSince, reportedAt)
	}
	if got[1].Name != "nginx" || got[1].State != "stopped" || !got[1].StateSince.Equal(nginxSince) || !got[1].LastReported.Equal(reportedAt) {
		t.Errorf("nginx row: got %+v, want name=nginx state=stopped state_since=%v last_reported=%v",
			got[1], nginxSince, reportedAt)
	}
}

// A second call with the same (device_id, service_name) must REPLACE
// the prior row (latest state wins). Without UPSERT semantics the
// dashboard would show stale data forever.
func TestRegistryRecordServiceStatesUpsertsOnRepeat(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-svcstatus-02", "77777777-7777-8888-8888-999999999999")

	first := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)

	// nginx running first.
	if err := srv.Registry.RecordServiceStates(ctx, deviceID, []servicestatus.ServiceState{
		{Name: "nginx", State: service.StateRunning, StateSince: first},
	}, first); err != nil {
		t.Fatalf("first RecordServiceStates: %v", err)
	}

	// Five minutes later, nginx stops.
	if err := srv.Registry.RecordServiceStates(ctx, deviceID, []servicestatus.ServiceState{
		{Name: "nginx", State: service.StateStopped, StateSince: second},
	}, second); err != nil {
		t.Fatalf("second RecordServiceStates: %v", err)
	}

	var (
		state        string
		stateSince   time.Time
		lastReported time.Time
		count        int
	)
	if err := srv.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM device_services WHERE device_id = $1 AND service_name = 'nginx'`, deviceID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("rows for nginx: got %d, want 1 (UPSERT should replace, not append)", count)
	}
	if err := srv.Pool.QueryRow(ctx, `
		SELECT state, state_since, last_reported FROM device_services WHERE device_id = $1 AND service_name = 'nginx'
	`, deviceID).Scan(&state, &stateSince, &lastReported); err != nil {
		t.Fatalf("read latest: %v", err)
	}
	if state != "stopped" || !stateSince.Equal(second) || !lastReported.Equal(second) {
		t.Errorf("latest row: got state=%s state_since=%v last_reported=%v, want state=stopped state_since=%v last_reported=%v",
			state, stateSince, lastReported, second, second)
	}
}

// Unknown / malformed device_id → ErrDeviceNotFound, no rows inserted.
// The ingester poisons on this so a decommissioned device's late
// report doesn't loop in the queue.
func TestRegistryRecordServiceStatesUnknownDevice(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// A well-formed UUID that's never been enrolled.
	ghostID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	err := srv.Registry.RecordServiceStates(ctx, ghostID, []servicestatus.ServiceState{
		{Name: "nginx", State: service.StateRunning, StateSince: time.Now()},
	}, time.Now())
	if !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("ghost device: got %v, want ErrDeviceNotFound", err)
	}

	// Not even a UUID.
	err = srv.Registry.RecordServiceStates(ctx, "not-a-uuid", []servicestatus.ServiceState{
		{Name: "nginx", State: service.StateRunning, StateSince: time.Now()},
	}, time.Now())
	if !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("non-uuid: got %v, want ErrDeviceNotFound", err)
	}
}

// An empty Services slice is a valid no-op (a successfully-received
// report that happens to contain no allow-listed services). No error,
// no rows inserted, no row for the device exists yet.
func TestRegistryRecordServiceStatesEmptyIsNoOp(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-svcstatus-03", "88888888-8888-9999-9999-aaaaaaaaaaaa")

	if err := srv.Registry.RecordServiceStates(ctx, deviceID, nil, time.Now()); err != nil {
		t.Fatalf("RecordServiceStates with nil slice: %v", err)
	}
	var count int
	if err := srv.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM device_services WHERE device_id = $1`, deviceID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("device_services rows after empty report: got %d, want 0", count)
	}
}

// The dashboard's Services panel pulls from the same per-device endpoint
// it already polls every 10s. GET /devices/{id} must surface the rows
// written by RecordServiceStates as a "services" array ordered by name,
// with state, state_since, and last_reported all round-tripped as the
// expected types (string state, RFC3339 timestamps).
func TestGetDeviceByIDIncludesServices(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-svcstatus-api", "99999999-9999-aaaa-bbbb-cccccccccccc")

	reportedAt := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	if err := srv.Registry.RecordServiceStates(ctx, deviceID, []servicestatus.ServiceState{
		{Name: "com.uknomi.edge-ui", State: service.StateRunning, StateSince: reportedAt.Add(-2 * time.Hour)},
		{Name: "nginx", State: service.StateStopped, StateSince: reportedAt.Add(-30 * time.Second)},
	}, reportedAt); err != nil {
		t.Fatalf("RecordServiceStates: %v", err)
	}

	resp := doDeviceGet(t, srv.URL, deviceID, mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /devices/%s: status %d; body=%s", deviceID, resp.StatusCode, raw)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rawServices, ok := body["services"].([]any)
	if !ok {
		t.Fatalf("response missing 'services' array; got body keys: %v", keysOf(body))
	}
	if len(rawServices) != 2 {
		t.Fatalf("services: got %d entries, want 2", len(rawServices))
	}

	// Services must be ordered by name so the dashboard rendering is
	// stable across reads.
	first := rawServices[0].(map[string]any)
	if got := first["name"]; got != "com.uknomi.edge-ui" {
		t.Errorf("services[0].name: got %v, want com.uknomi.edge-ui", got)
	}
	if got := first["state"]; got != "running" {
		t.Errorf("services[0].state: got %v, want running", got)
	}
	if _, err := time.Parse(time.RFC3339, first["state_since"].(string)); err != nil {
		t.Errorf("services[0].state_since not RFC3339: %v (got %v)", err, first["state_since"])
	}
	if _, err := time.Parse(time.RFC3339, first["last_reported"].(string)); err != nil {
		t.Errorf("services[0].last_reported not RFC3339: %v (got %v)", err, first["last_reported"])
	}

	second := rawServices[1].(map[string]any)
	if got := second["name"]; got != "nginx" {
		t.Errorf("services[1].name: got %v, want nginx", got)
	}
	if got := second["state"]; got != "stopped" {
		t.Errorf("services[1].state: got %v, want stopped", got)
	}
}

// A device with no service-status report yet must serialize "services"
// as [] not null. The dashboard's render code distinguishes "no report
// yet" (empty array) from "not present" (missing field).
func TestGetDeviceByIDServicesEmptyArrayNotNull(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-svcstatus-empty", "bbbbbbbb-cccc-dddd-eeee-ffffffffffff")

	resp := doDeviceGet(t, srv.URL, deviceID, mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /devices/%s: status %d; body=%s", deviceID, resp.StatusCode, raw)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, present := body["services"]
	if !present {
		t.Fatalf("response missing 'services' field; want [] for a device with no reports")
	}
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("services: got %T (%v), want []any (empty array, not null)", got, got)
	}
	if len(arr) != 0 {
		t.Errorf("services: got %d entries for an un-reported device, want 0", len(arr))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
