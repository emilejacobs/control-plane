package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// TestRegistryALPRLicenseRoundTrip — #84 storage cycle. The per-device Plate
// Recognizer license is a secret: SetALPRLicense stores it and GetALPRLicense
// (used only by Commission) round-trips it, while the device read path exposes
// only ALPRLicenseSet — never the value.
func TestRegistryALPRLicenseRoundTrip(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-alpr-01", "11111111-2222-3333-4444-bbbbbbbbbbbb")
	ctx = staffCtx(ctx)

	// Fresh device: no license, GetByID reports not-set.
	d, err := srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID (fresh): %v", err)
	}
	if d.ALPRLicenseSet {
		t.Error("fresh device ALPRLicenseSet: got true, want false")
	}
	if lic, err := srv.Registry.GetALPRLicense(ctx, deviceID); err != nil || lic != "" {
		t.Errorf("GetALPRLicense (fresh): got (%q,%v), want (\"\",nil)", lic, err)
	}

	// Set the license: GetByID flips to set; GetALPRLicense returns the raw value.
	const secret = "ALPR-LICENSE-SECRET-123"
	if err := srv.Registry.SetALPRLicense(ctx, deviceID, secret); err != nil {
		t.Fatalf("SetALPRLicense: %v", err)
	}
	d, err = srv.Registry.GetByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetByID (after set): %v", err)
	}
	if !d.ALPRLicenseSet {
		t.Error("ALPRLicenseSet after set: got false, want true")
	}
	if lic, err := srv.Registry.GetALPRLicense(ctx, deviceID); err != nil || lic != secret {
		t.Errorf("GetALPRLicense (after set): got (%q,%v), want (%q,nil)", lic, err, secret)
	}

	// Unknown / non-UUID device → ErrDeviceNotFound on both write and read.
	if err := srv.Registry.SetALPRLicense(ctx, "not-a-uuid", "x"); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("SetALPRLicense(bad id): got %v, want ErrDeviceNotFound", err)
	}
	if _, err := srv.Registry.GetALPRLicense(ctx, "not-a-uuid"); !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Errorf("GetALPRLicense(bad id): got %v, want ErrDeviceNotFound", err)
	}
}
