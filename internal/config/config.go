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
