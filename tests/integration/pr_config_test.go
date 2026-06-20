package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

// TestRegistryPRConfigGetUpsert — Plate Recognizer per-device config slice
// (issue #5, ADR-030 § 3). A fresh device has no PR config (exists=false);
// UpsertPRConfig inserts then replaces the editable subset, round-tripping
// region/camera_id/caching/image and the inline webhooks verbatim.
func TestRegistryPRConfigGetUpsert(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-pr-01", "11111111-2222-3333-4444-dddddddddddd")
	ctx = staffCtx(ctx)

	// Fresh device: no PR config row.
	if _, ok, err := srv.Registry.GetPRConfig(ctx, deviceID); err != nil {
		t.Fatalf("GetPRConfig (fresh): %v", err)
	} else if ok {
		t.Fatal("fresh device should have no PR config (exists=true)")
	}

	// Insert.
	in := prconfig.Config{
		CameraID: "0",
		Region:   "us-az",
		Webhooks: []prconfig.Webhook{
			{Name: "prod", URL: "https://api-flask.uknomi.com/recognize_vehicle_event", Enabled: true, Image: true, Caching: false},
			{Name: "pre-prod", URL: "https://preprod-flask.uknomi.com/recognize_vehicle_event", Enabled: false, Image: false, Caching: true},
		},
	}
	got, err := srv.Registry.UpsertPRConfig(ctx, deviceID, in)
	if err != nil {
		t.Fatalf("UpsertPRConfig (insert): %v", err)
	}
	if got.Region != "us-az" || got.CameraID != "0" {
		t.Errorf("inserted scalar fields: got %+v", got)
	}
	if len(got.Webhooks) != 2 ||
		got.Webhooks[0].Name != "prod" || !got.Webhooks[0].Enabled || !got.Webhooks[0].Image ||
		got.Webhooks[1].Name != "pre-prod" || got.Webhooks[1].Enabled || !got.Webhooks[1].Caching {
		t.Errorf("inserted webhooks: got %+v", got.Webhooks)
	}
	if got.LastAppliedAt != nil || got.LastAppliedCorrID != "" {
		t.Errorf("last_applied_* should be unset before any apply-ack: got at=%v corr=%q", got.LastAppliedAt, got.LastAppliedCorrID)
	}

	// GetPRConfig now reports exists=true with the same data.
	fetched, ok, err := srv.Registry.GetPRConfig(ctx, deviceID)
	if err != nil || !ok {
		t.Fatalf("GetPRConfig (after upsert): ok=%v err=%v", ok, err)
	}
	if fetched.Region != "us-az" || len(fetched.Webhooks) != 2 {
		t.Errorf("fetched config: got %+v", fetched)
	}

	// Upsert again (replace): change region, drop a webhook.
	in.Region = "us-ca"
	in.Webhooks = []prconfig.Webhook{{Name: "prod", URL: "https://api-flask.uknomi.com/recognize_vehicle_event", Enabled: true, Image: true}}
	got, err = srv.Registry.UpsertPRConfig(ctx, deviceID, in)
	if err != nil {
		t.Fatalf("UpsertPRConfig (replace): %v", err)
	}
	if got.Region != "us-ca" {
		t.Errorf("replaced scalars: got %+v", got)
	}
	if len(got.Webhooks) != 1 || got.Webhooks[0].Name != "prod" {
		t.Errorf("replaced webhooks: got %+v", got.Webhooks)
	}

	// RecordPRConfigApplied stamps last_applied_* (clears the dashboard
	// "Pending"); GetPRConfig surfaces them. Upsert must NOT clear them.
	if got.LastAppliedAt != nil {
		t.Errorf("last_applied_at should be nil before an apply-ack: %v", got.LastAppliedAt)
	}
	if err := srv.Registry.RecordPRConfigApplied(ctx, deviceID, "corr-xyz", time.Now()); err != nil {
		t.Fatalf("RecordPRConfigApplied: %v", err)
	}
	after, _, err := srv.Registry.GetPRConfig(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetPRConfig after apply: %v", err)
	}
	if after.LastAppliedAt == nil || after.LastAppliedCorrID != "corr-xyz" {
		t.Errorf("apply not stamped: at=%v corr=%q", after.LastAppliedAt, after.LastAppliedCorrID)
	}
}
