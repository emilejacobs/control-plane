package enroll_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/enroll"
	"github.com/emilejacobs/control-plane/internal/config"
)

// fakeCP stands in for cp-api's POST /enrollments. It records the last
// request it saw so tests can assert the device sent the right shape, and
// replies 201 with a canned enrollment response.
type fakeCP struct {
	srv *httptest.Server

	gotMethod         string
	gotPath           string
	gotIdempotencyKey string
	gotContentType    string
	gotBody           map[string]any
	calls             int
}

func newFakeCP(t *testing.T) *fakeCP {
	t.Helper()
	f := &fakeCP{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		f.gotMethod = r.Method
		f.gotPath = r.URL.Path
		f.gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		f.gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &f.gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_id":            "dev_abc123",
			"mtls_cert_pem":        "-----BEGIN CERTIFICATE-----\nFAKECERT\n-----END CERTIFICATE-----\n",
			"mtls_private_key_pem": "-----BEGIN PRIVATE KEY-----\nFAKEKEY\n-----END PRIVATE KEY-----\n",
			"iot_endpoint":         "agcw133a9fxn7-ats.iot.us-east-1.amazonaws.com",
			"iot_thing_arn":        "arn:aws:iot:us-east-1:1234:thing/dev_abc123",
			"mtls_cert_expires_at": "2049-01-01T00:00:00Z",
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func sampleParams(cpURL, runtimeDir string) enroll.Params {
	return enroll.Params{
		CPBaseURL:    cpURL,
		BootstrapKey: "bootstrap-secret",
		Hardware: enroll.Hardware{
			Hostname:     "07-eegees-mesa-macmini",
			HardwareUUID: "HW-UUID-1",
			HardwareKind: "mac",
			OSVersion:    "macOS 14.6.1",
			AgentVersion: "1.5.0",
		},
		CACertPEM:  []byte("-----BEGIN CERTIFICATE-----\nAMAZONROOTCA\n-----END CERTIFICATE-----\n"),
		RuntimeDir: runtimeDir,
		BrokerURL:  "tls://agcw133a9fxn7-ats.iot.us-east-1.amazonaws.com:8883",
		Defaults: config.Config{
			TelemetryInterval:     "30s",
			ServiceAllowList:      []string{"com.uknomi.edge-ui", "com.tailscale.tailscaled"},
			ServiceStatusInterval: "5m",
			CamerasPath:           "/usr/local/etc/uknomi/cameras.json",
			SnapshotStatePath:     "/var/uknomi/snapshot-state.json",
			ProbeInterval:         "5m",
			AutoLoginUser:         "uknomi",
		},
	}
}

// The happy path: Enroll posts a well-formed request (with the hardware-UUID
// idempotency key), then installs the returned cert/key + the bundled CA and
// writes a loadable agent-config from the response + the operational defaults.
func TestEnrollHappyPath(t *testing.T) {
	cp := newFakeCP(t)
	dir := t.TempDir()

	res, err := enroll.Enroll(context.Background(), sampleParams(cp.srv.URL, dir))
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	// --- request shape the device sent ---
	if cp.gotMethod != http.MethodPost || cp.gotPath != "/enrollments" {
		t.Errorf("request line: got %s %s want POST /enrollments", cp.gotMethod, cp.gotPath)
	}
	if cp.gotIdempotencyKey != "HW-UUID-1" {
		t.Errorf("Idempotency-Key: got %q want the hardware UUID", cp.gotIdempotencyKey)
	}
	if cp.gotContentType != "application/json" {
		t.Errorf("Content-Type: got %q", cp.gotContentType)
	}
	for k, want := range map[string]string{
		"bootstrap_key": "bootstrap-secret",
		"hostname":      "07-eegees-mesa-macmini",
		"hardware_uuid": "HW-UUID-1",
		"hardware_kind": "mac",
		"os_version":    "macOS 14.6.1",
		"agent_version": "1.5.0",
	} {
		if got, _ := cp.gotBody[k].(string); got != want {
			t.Errorf("request body[%q]: got %q want %q", k, got, want)
		}
	}

	// --- result ---
	if res.DeviceID != "dev_abc123" {
		t.Errorf("DeviceID: got %q", res.DeviceID)
	}

	// --- cert / key / ca on disk ---
	assertFile(t, filepath.Join(dir, "cert.pem"), "FAKECERT", 0o600)
	assertFile(t, filepath.Join(dir, "key.pem"), "FAKEKEY", 0o600)
	assertFile(t, filepath.Join(dir, "ca.pem"), "AMAZONROOTCA", 0o644)

	// --- agent-config.json is loadable and carries response + defaults ---
	cfgPath := filepath.Join(dir, "agent-config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("written agent-config is not loadable: %v", err)
	}
	if cfg.DeviceID != "dev_abc123" || cfg.ClientID != "dev_abc123" {
		t.Errorf("config device/client id: got %q/%q", cfg.DeviceID, cfg.ClientID)
	}
	if cfg.Version != "1.5.0" {
		t.Errorf("config version: got %q want the agent version", cfg.Version)
	}
	if cfg.BrokerURL != "tls://agcw133a9fxn7-ats.iot.us-east-1.amazonaws.com:8883" {
		t.Errorf("config broker_url: got %q", cfg.BrokerURL)
	}
	if cfg.CertPath != filepath.Join(dir, "cert.pem") ||
		cfg.KeyPath != filepath.Join(dir, "key.pem") ||
		cfg.CACertPath != filepath.Join(dir, "ca.pem") {
		t.Errorf("config cert paths: %q %q %q", cfg.CertPath, cfg.KeyPath, cfg.CACertPath)
	}
	if cfg.CamerasPath != "/usr/local/etc/uknomi/cameras.json" ||
		cfg.SnapshotStatePath != "/var/uknomi/snapshot-state.json" ||
		cfg.AutoLoginUser != "uknomi" || len(cfg.ServiceAllowList) != 2 {
		t.Errorf("config defaults not carried through: %+v", cfg)
	}
	if res.ConfigPath != cfgPath {
		t.Errorf("ConfigPath: got %q want %q", res.ConfigPath, cfgPath)
	}
}

func assertFile(t *testing.T, path, wantSubstr string, wantMode os.FileMode) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !contains(string(raw), wantSubstr) {
		t.Errorf("%s: content %q missing %q", path, raw, wantSubstr)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := st.Mode().Perm(); got != wantMode {
		t.Errorf("%s mode: got %o want %o", path, got, wantMode)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
