package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	DeviceID   string `json:"device_id"`
	Version    string `json:"version"`
	BrokerURL  string `json:"broker_url"`
	ClientID   string `json:"client_id"`
	CertPath   string `json:"cert_path"`
	KeyPath    string `json:"key_path"`
	CACertPath string `json:"ca_cert_path"`
	// TelemetryInterval is parsed with time.ParseDuration (e.g. "30s"). Empty
	// or absent means use the agent's default (30s).
	TelemetryInterval string `json:"telemetry_interval,omitempty"`

	// ServiceAllowList is the Phase 2 per-device list of services to report
	// status on (launchd unit names on Mac, systemd unit names on Linux).
	// Empty / absent disables service-status reporting entirely — safe
	// default for agents shipped before the per-OS bundle is decided.
	ServiceAllowList []string `json:"service_allow_list,omitempty"`

	// ServiceStatusInterval is parsed with time.ParseDuration (e.g. "5m").
	// Empty / absent means use the agent's default (5m). Ignored when
	// ServiceAllowList is empty.
	ServiceStatusInterval string `json:"service_status_interval,omitempty"`

	// CamerasPath is the on-disk location for the cameras.json file
	// the agent's cameras.update handler writes (and the device-local
	// Edge UI reads). Empty / absent disables the cameras.update
	// handler — agent.New self-skips the handler registration when the
	// field is unset, so devices not yet provisioned by the new install
	// module retain the pre-Phase-2-Edge-UI shape.
	CamerasPath string `json:"cameras_path,omitempty"`

	// SnapshotStatePath is the on-disk location for the agent's snapshot
	// state file (#9): persisted scheduled-snapshot cadence + per-camera
	// next-fire times. Empty / absent disables the snapshot.config handler
	// (and the scheduler) — devices not yet provisioned for scheduled
	// snapshots retain the prior shape.
	SnapshotStatePath string `json:"snapshot_state_path,omitempty"`

	// ProbeInterval is parsed with time.ParseDuration (e.g. "5m"). It
	// sets the cadence of the Phase 2 fleet-health-probes reporter
	// (issue #19). Empty defaults to 5 minutes.
	ProbeInterval string `json:"probe_interval,omitempty"`

	// AutoLoginUser is the user the device is expected to auto-login as
	// (e.g. "uknomi"); the auto_login / gui_session probes compare the
	// observed state against it. Empty disables those probes' user match.
	AutoLoginUser string `json:"auto_login_user,omitempty"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}
