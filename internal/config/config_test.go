package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/config"
)

func TestConfigLoad(t *testing.T) {
	raw := `{
		"device_id": "dev-001",
		"version": "0.1.0",
		"broker_url": "tls://example.com:8883",
		"client_id": "client-1",
		"cert_path": "/etc/uknomi/cert.pem",
		"key_path": "/etc/uknomi/key.pem",
		"ca_cert_path": "/etc/uknomi/ca.pem"
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DeviceID != "dev-001" {
		t.Errorf("DeviceID: got %q, want dev-001", cfg.DeviceID)
	}
	if cfg.BrokerURL != "tls://example.com:8883" {
		t.Errorf("BrokerURL: got %q", cfg.BrokerURL)
	}
	if cfg.CertPath != "/etc/uknomi/cert.pem" {
		t.Errorf("CertPath: got %q", cfg.CertPath)
	}
	if cfg.Version != "0.1.0" {
		t.Errorf("Version: got %q", cfg.Version)
	}
}

func TestConfigLoadMissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/config.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/config.json") {
		t.Errorf("error should name the missing path; got: %v", err)
	}
}

func TestConfigLoadMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}
