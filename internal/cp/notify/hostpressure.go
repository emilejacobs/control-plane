package notify

import (
	"context"
	"strconv"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// *HostPressureThresholdSource is the production ingest.HostPressureThresholdSource.
var _ ingest.HostPressureThresholdSource = (*HostPressureThresholdSource)(nil)

// HostPressureThresholdSource reads the CP-tunable host_net_pressure scoring
// thresholds from cp_settings. It implements ingest.HostPressureThresholdSource
// and is read by the ingester per report, so an operator's Settings edit
// applies fleet-wide on the next probe (~5 min) with no agent redeploy. Each
// field falls back independently to the calibrated default.
type HostPressureThresholdSource struct {
	store SettingStore
}

func NewHostPressureThresholdSource(store SettingStore) *HostPressureThresholdSource {
	return &HostPressureThresholdSource{store: store}
}

func (s *HostPressureThresholdSource) HostPressureThresholds(ctx context.Context) healthprobes.HostPressureThresholds {
	d := healthprobes.DefaultHostPressureThresholds
	return healthprobes.HostPressureThresholds{
		EphemeralWarnPct: s.float(ctx, registry.SettingHostPressureEphemeralWarnPct, d.EphemeralWarnPct),
		EphemeralCritPct: s.float(ctx, registry.SettingHostPressureEphemeralCritPct, d.EphemeralCritPct),
		CloseWaitWarn:    s.int(ctx, registry.SettingHostPressureCloseWaitWarn, d.CloseWaitWarn),
		CloseWaitCrit:    s.int(ctx, registry.SettingHostPressureCloseWaitCrit, d.CloseWaitCrit),
	}
}

func (s *HostPressureThresholdSource) float(ctx context.Context, key string, fallback float64) float64 {
	raw, _, err := s.store.GetCPSetting(ctx, key)
	if err != nil || raw == "" {
		return fallback
	}
	if n, err := strconv.ParseFloat(raw, 64); err == nil && n >= 0 {
		return n
	}
	return fallback
}

func (s *HostPressureThresholdSource) int(ctx context.Context, key string, fallback int) int {
	raw, _, err := s.store.GetCPSetting(ctx, key)
	if err != nil || raw == "" {
		return fallback
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
		return n
	}
	return fallback
}
