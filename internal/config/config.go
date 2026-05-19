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
