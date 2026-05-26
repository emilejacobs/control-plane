package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
)

// Phase 2 Edge UI rework cycle 19: end-to-end cmd-result ACK landing
// flows through Registry.RecordCamerasApplied and is readable via
// GetCamerasStatus. The dashboard's CamerasPanel reads the same
// fields back to flip its "pending" badge to applied.
func TestRegistryRecordCamerasApplied(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cam-ack-01", "55555555-2222-3333-4444-cccccccccccc")

	// Fresh device: no ACK landed yet, both fields nil.
	status, err := srv.Registry.GetCamerasStatus(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetCamerasStatus (fresh): %v", err)
	}
	if status.LastAppliedAt != nil || status.LastAppliedCorrelationID != nil {
		t.Errorf("fresh device cameras status: got %+v, want zero", status)
	}

	at := time.Date(2026, 5, 26, 12, 30, 0, 0, time.UTC)
	if err := srv.Registry.RecordCamerasApplied(ctx, deviceID, "corr-cam-1", at); err != nil {
		t.Fatalf("RecordCamerasApplied: %v", err)
	}

	status, err = srv.Registry.GetCamerasStatus(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetCamerasStatus (after ACK): %v", err)
	}
	if status.LastAppliedAt == nil || !status.LastAppliedAt.Equal(at) {
		t.Errorf("LastAppliedAt: got %v, want %v", status.LastAppliedAt, at)
	}
	if status.LastAppliedCorrelationID == nil || *status.LastAppliedCorrelationID != "corr-cam-1" {
		t.Errorf("LastAppliedCorrelationID: got %v, want corr-cam-1", status.LastAppliedCorrelationID)
	}

	// Latest-wins: a second ACK overwrites.
	at2 := at.Add(3 * time.Minute)
	if err := srv.Registry.RecordCamerasApplied(ctx, deviceID, "corr-cam-2", at2); err != nil {
		t.Fatalf("RecordCamerasApplied (2): %v", err)
	}
	status, _ = srv.Registry.GetCamerasStatus(ctx, deviceID)
	if !status.LastAppliedAt.Equal(at2) || *status.LastAppliedCorrelationID != "corr-cam-2" {
		t.Errorf("after second ACK: got %v / %v, want %v / corr-cam-2",
			status.LastAppliedAt, status.LastAppliedCorrelationID, at2)
	}
}

// Unknown device → ErrDeviceNotFound so cp-ingest can DLQ a late
// ACK from a decommissioned device rather than looping on it.
func TestRegistryRecordCamerasAppliedUnknownDevice(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	err := srv.Registry.RecordCamerasApplied(
		ctx,
		"66666666-6666-6666-6666-666666666666",
		"corr",
		time.Now(),
	)
	if !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("expected ErrDeviceNotFound, got %v", err)
	}
}

// End-to-end: CmdResultIngester routes a cameras.update success
// ACK to RecordCamerasApplied → GetCamerasStatus surfaces the
// stamps. This is the cycle that closes the loop: an ACK landing
// on the SQS queue ends up visible to the dashboard via GET
// /devices/{id}/cameras' last_applied_* fields.
func TestCmdResultCamerasAckEndToEnd(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-cam-e2e-01", "77777777-2222-3333-4444-cccccccccccc")

	at := time.Date(2026, 5, 26, 12, 45, 0, 0, time.UTC)
	ing := ingest.NewCmdResultIngester(srv.Registry, func() time.Time { return at })

	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: "corr-e2e-cam",
			CommandID:     "cmd-e2e-cam",
			Type:          "cameras.update",
			Success:       true,
		},
		DeviceID: deviceID,
	}
	if err := ing.Handle(ctx, msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	status, err := srv.Registry.GetCamerasStatus(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetCamerasStatus: %v", err)
	}
	if status.LastAppliedAt == nil || !status.LastAppliedAt.Equal(at) {
		t.Errorf("LastAppliedAt after ingester: got %v, want %v",
			status.LastAppliedAt, at)
	}
	if status.LastAppliedCorrelationID == nil || *status.LastAppliedCorrelationID != "corr-e2e-cam" {
		t.Errorf("LastAppliedCorrelationID: got %v, want corr-e2e-cam",
			status.LastAppliedCorrelationID)
	}
}
