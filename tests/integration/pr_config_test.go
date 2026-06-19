package integration_test

import (
	"context"
	"testing"

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
		Caching:  false,
		Image:    true,
		Webhooks: []prconfig.Webhook{
			{Name: "prod", URL: "https://api-flask.uknomi.com/recognize_vehicle_event", Enabled: true},
			{Name: "pre-prod", URL: "https://preprod-flask.uknomi.com/recognize_vehicle_event", Enabled: false},
		},
	}
	got, err := srv.Registry.UpsertPRConfig(ctx, deviceID, in)
	if err != nil {
		t.Fatalf("UpsertPRConfig (insert): %v", err)
	}
	if got.Region != "us-az" || got.CameraID != "0" || got.Caching || !got.Image {
		t.Errorf("inserted scalar fields: got %+v", got)
	}
	if len(got.Webhooks) != 2 || got.Webhooks[0].Name != "prod" || !got.Webhooks[0].Enabled ||
		got.Webhooks[1].Name != "pre-prod" || got.Webhooks[1].Enabled {
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

	// Upsert again (replace): change region + caching, drop a webhook.
	in.Region = "us-ca"
	in.Caching = true
	in.Webhooks = []prconfig.Webhook{{Name: "prod", URL: "https://api-flask.uknomi.com/recognize_vehicle_event", Enabled: true}}
	got, err = srv.Registry.UpsertPRConfig(ctx, deviceID, in)
	if err != nil {
		t.Fatalf("UpsertPRConfig (replace): %v", err)
	}
	if got.Region != "us-ca" || !got.Caching {
		t.Errorf("replaced scalars: got %+v", got)
	}
	if len(got.Webhooks) != 1 || got.Webhooks[0].Name != "prod" {
		t.Errorf("replaced webhooks: got %+v", got.Webhooks)
	}
}
