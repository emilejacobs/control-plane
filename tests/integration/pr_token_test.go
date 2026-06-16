package integration_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// TestRegistryCPSettingRoundTrip — #84 storage cycle for the CP-singleton
// settings store. The account-wide PR token starts unset, persists on set, and
// upserts on re-set. The value is a secret; the API exposes only is_set.
func TestRegistryCPSettingRoundTrip(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// Unset: not found, empty value.
	if v, ok, err := srv.Registry.GetCPSetting(ctx, registry.SettingPlateRecognizerToken); err != nil || ok || v != "" {
		t.Fatalf("GetCPSetting (unset): got (%q,%v,%v), want (\"\",false,nil)", v, ok, err)
	}

	// Set: persists.
	if err := srv.Registry.SetCPSetting(ctx, registry.SettingPlateRecognizerToken, "pr-token-secret"); err != nil {
		t.Fatalf("SetCPSetting: %v", err)
	}
	if v, ok, err := srv.Registry.GetCPSetting(ctx, registry.SettingPlateRecognizerToken); err != nil || !ok || v != "pr-token-secret" {
		t.Fatalf("GetCPSetting (after set): got (%q,%v,%v), want (\"pr-token-secret\",true,nil)", v, ok, err)
	}

	// Re-set: upsert replaces the value (single row).
	if err := srv.Registry.SetCPSetting(ctx, registry.SettingPlateRecognizerToken, "pr-token-v2"); err != nil {
		t.Fatalf("SetCPSetting (upsert): %v", err)
	}
	if v, _, _ := srv.Registry.GetCPSetting(ctx, registry.SettingPlateRecognizerToken); v != "pr-token-v2" {
		t.Errorf("GetCPSetting (after upsert): got %q, want pr-token-v2", v)
	}
}
