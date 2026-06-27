package notify

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

type mapSettingStore map[string]string

func (m mapSettingStore) GetCPSetting(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}

// Unset settings yield the calibrated defaults.
func TestHostPressureThresholds_UnsetDefaults(t *testing.T) {
	src := NewHostPressureThresholdSource(mapSettingStore{})
	got := src.HostPressureThresholds(context.Background())
	if got != healthprobes.DefaultHostPressureThresholds {
		t.Errorf("got %+v, want defaults %+v", got, healthprobes.DefaultHostPressureThresholds)
	}
}

// Configured values override the defaults; an invalid field falls back to its
// own default without poisoning the others.
func TestHostPressureThresholds_OverrideAndFallback(t *testing.T) {
	src := NewHostPressureThresholdSource(mapSettingStore{
		registry.SettingHostPressureEphemeralWarnPct: "50",
		registry.SettingHostPressureEphemeralCritPct: "75",
		registry.SettingHostPressureCloseWaitWarn:    "garbage", // invalid → default
		registry.SettingHostPressureCloseWaitCrit:    "1000",
	})
	got := src.HostPressureThresholds(context.Background())
	want := healthprobes.HostPressureThresholds{
		EphemeralWarnPct: 50,
		EphemeralCritPct: 75,
		CloseWaitWarn:    healthprobes.DefaultHostPressureThresholds.CloseWaitWarn, // fell back
		CloseWaitCrit:    1000,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
