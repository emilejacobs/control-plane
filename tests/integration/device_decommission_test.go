package integration_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// TestRegistryDeleteDevice — decommissioning removes the device row and
// cascades to its child rows (health probes etc.); an unknown id returns
// ErrDeviceNotFound.
func TestRegistryDeleteDevice(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	deviceID := enrollForTest(t, srv, "mac-decomm", "eeeeeeee-0000-0000-0000-000000000001")
	if err := srv.Registry.RecordHealthProbes(ctx, deviceID, []healthprobes.Result{
		{Name: "plate_recognizer_container", Status: healthprobes.StatusRed, State: "exited"},
	}, time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed probe: %v", err)
	}

	if err := srv.Registry.DeleteDevice(ctx, deviceID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	// Device row gone.
	var count int
	if err := srv.Pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE id = $1`, deviceID).Scan(&count); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if count != 0 {
		t.Errorf("device row count = %d, want 0", count)
	}
	// Child rows cascaded away.
	if err := srv.Pool.QueryRow(ctx, `SELECT count(*) FROM device_health_probes WHERE device_id = $1`, deviceID).Scan(&count); err != nil {
		t.Fatalf("count probes: %v", err)
	}
	if count != 0 {
		t.Errorf("health-probe rows = %d, want 0 (ON DELETE CASCADE)", count)
	}

	if err := srv.Registry.DeleteDevice(ctx, deviceID); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("DeleteDevice(already gone) err = %v, want ErrDeviceNotFound", err)
	}
	if err := srv.Registry.DeleteDevice(ctx, "not-a-uuid"); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("DeleteDevice(non-uuid) err = %v, want ErrDeviceNotFound", err)
	}
}

// TestDeleteDeviceEndpoint — DELETE /devices/{id} is staff-only and removes
// the device through the real router.
func TestDeleteDeviceEndpoint(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-decomm-api", "eeeeeeee-0000-0000-0000-000000000002")

	// Non-staff operator is forbidden.
	_, scopedTok := enrolledOperator(t, ctx, srv, "scoped-decomm@acme.test", false)
	if code := doDelete(t, srv.URL+"/devices/"+deviceID, scopedTok); code != http.StatusForbidden {
		t.Errorf("non-staff DELETE = %d, want 403", code)
	}

	// Staff deletes it.
	staffTok := mintAccessToken(t, ctx, srv)
	if code := doDelete(t, srv.URL+"/devices/"+deviceID, staffTok); code != http.StatusNoContent {
		t.Fatalf("staff DELETE = %d, want 204", code)
	}
	if code := doDelete(t, srv.URL+"/devices/"+deviceID, staffTok); code != http.StatusNotFound {
		t.Errorf("DELETE already-gone = %d, want 404", code)
	}
}

func doDelete(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "decomm-"+url)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
