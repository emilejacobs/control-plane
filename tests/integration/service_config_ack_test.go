package integration_test

import (
	"context"
	"testing"
	"time"
)

// Phase 2 slice 2 cycle 10: cmd-result ACK landing flows through
// Registry.RecordServiceConfigApplied. A subsequent GetServiceConfig
// surfaces the applied timestamp + correlation_id so the dashboard's
// EditServicesModal can show "applied" against the operator's most
// recent PUT.
func TestRegistryRecordServiceConfigApplied(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-ack-01", "22222222-3333-4444-5555-cccccccccccc")
	scoped := staffCtx(ctx)

	at := time.Date(2026, 5, 24, 19, 5, 0, 0, time.UTC)
	if err := srv.Registry.RecordServiceConfigApplied(ctx, deviceID, "corr-applied-1", at); err != nil {
		t.Fatalf("RecordServiceConfigApplied: %v", err)
	}

	cfg, err := srv.Registry.GetServiceConfig(scoped, deviceID)
	if err != nil {
		t.Fatalf("GetServiceConfig: %v", err)
	}
	if cfg.LastAppliedAt == nil || !cfg.LastAppliedAt.Equal(at) {
		t.Errorf("LastAppliedAt: got %v, want %v", cfg.LastAppliedAt, at)
	}
	if cfg.LastAppliedCorrelationID == nil || *cfg.LastAppliedCorrelationID != "corr-applied-1" {
		t.Errorf("LastAppliedCorrelationID: got %v, want corr-applied-1", cfg.LastAppliedCorrelationID)
	}

	// A second ACK with a newer timestamp + different correlation_id
	// overwrites (latest-wins; idempotent on re-delivery of the same
	// (id, corr_id) tuple via UPDATE semantics).
	at2 := at.Add(2 * time.Minute)
	if err := srv.Registry.RecordServiceConfigApplied(ctx, deviceID, "corr-applied-2", at2); err != nil {
		t.Fatalf("RecordServiceConfigApplied (2): %v", err)
	}
	cfg, err = srv.Registry.GetServiceConfig(scoped, deviceID)
	if err != nil {
		t.Fatalf("GetServiceConfig (2): %v", err)
	}
	if !cfg.LastAppliedAt.Equal(at2) || *cfg.LastAppliedCorrelationID != "corr-applied-2" {
		t.Errorf("after second ACK: got %v/%v, want %v/corr-applied-2", cfg.LastAppliedAt, cfg.LastAppliedCorrelationID, at2)
	}
}

// Unknown device → ErrDeviceNotFound so cp-ingest can DLQ a late ACK
// from a decommissioned device rather than looping on it.
func TestRegistryRecordServiceConfigAppliedUnknownDevice(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	err := srv.Registry.RecordServiceConfigApplied(ctx, "44444444-4444-4444-4444-444444444444", "corr", time.Now())
	if err == nil {
		t.Fatal("expected ErrDeviceNotFound, got nil")
	}
}
