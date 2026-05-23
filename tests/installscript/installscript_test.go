// Package installscript_test drives the Linux install script
// (scripts/install-cp-agent.sh — Issue 22) through a sandboxed root +
// fake CP + stubbed systemctl. Each test exercises one behavior of the
// real script as run by an oncoming Pi/Radxa; the path that fires in
// production is the same path under test.
package installscript_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// scriptPath returns the absolute path to install-cp-agent.sh, computed
// from the test binary's location so a CI run + a local `go test` find
// the same file.
func scriptPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// tests/installscript → repo root → scripts/install-cp-agent.sh
	repo := filepath.Clean(filepath.Join(wd, "..", ".."))
	return filepath.Join(repo, "scripts", "install-cp-agent.sh")
}

// requireBash skips on platforms without a usable bash. macOS ships bash
// (3.x — old, but the script targets bash-compatible POSIX-ish features
// only). Pi/Radxa run bash. Windows would need WSL; the test skips.
func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash not available natively on Windows; install-cp-agent.sh targets Linux")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH")
	}
}

// captured remembers what the fake CP saw on its single expected
// /enrollments call: the headers, the JSON body, and the request count
// across all paths.
type captured struct {
	mu             sync.Mutex
	enrollHits     int
	idempotencyKey string
	authzHeader    string
	body           map[string]any
}

// fakeCP stands up an httptest server that satisfies the install
// script's POST /enrollments with the canonical Phase 1 response
// (mtls_cert_pem, mtls_private_key_pem, device_id, iot_endpoint,
// iot_thing_arn, mtls_cert_expires_at). It returns the server + the
// captured-state accessor.
func fakeCP(t *testing.T) (*httptest.Server, *captured) {
	t.Helper()
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/enrollments":
			c.mu.Lock()
			c.enrollHits++
			c.idempotencyKey = r.Header.Get("Idempotency-Key")
			c.authzHeader = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &c.body)
			c.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_id":             "dev-test-1",
				"mtls_cert_pem":         "-----BEGIN CERTIFICATE-----\nTESTCERT\n-----END CERTIFICATE-----\n",
				"mtls_private_key_pem":  "-----BEGIN PRIVATE KEY-----\nTESTKEY\n-----END PRIVATE KEY-----\n",
				"iot_endpoint":          "tls://iot.test:8883",
				"iot_thing_arn":         "arn:aws:iot:us-east-1:0:thing/dev-test-1",
				"mtls_cert_expires_at":  "2027-05-23T00:00:00Z",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

// sandboxRoot lays out a per-test fake Linux root under t.TempDir() that
// the script targets via env-var overrides. Returns the root path and a
// helper env slice the test passes to exec.Cmd.Env.
func sandboxRoot(t *testing.T, cp string) (root string, env []string) {
	t.Helper()
	root = t.TempDir()
	// Fake machine-id + os-release.
	machineIDDir := filepath.Join(root, "etc")
	if err := os.MkdirAll(machineIDDir, 0o755); err != nil {
		t.Fatalf("mkdir etc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(machineIDDir, "machine-id"),
		[]byte("11111111111122223333444455556666\n"), 0o644); err != nil {
		t.Fatalf("write machine-id: %v", err)
	}
	if err := os.WriteFile(filepath.Join(machineIDDir, "os-release"),
		[]byte(`PRETTY_NAME="Raspberry Pi OS Bookworm"`+"\n"), 0o644); err != nil {
		t.Fatalf("write os-release: %v", err)
	}
	// Fake agent binary the script will install.
	agentBin := filepath.Join(root, "uknomi-agent.bin")
	if err := os.WriteFile(agentBin, []byte("#!/bin/sh\necho fake agent\n"), 0o755); err != nil {
		t.Fatalf("write agent bin: %v", err)
	}
	// Bootstrap key file.
	keyFile := filepath.Join(root, "bootstrap.key")
	if err := os.WriteFile(keyFile, []byte("test-bootstrap-key-do-not-log\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	// systemctl stub on PATH — records its argv to a file so the test
	// can assert against it without needing real systemd.
	stubDir := filepath.Join(root, "stubs")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatalf("mkdir stubs: %v", err)
	}
	stubLog := filepath.Join(root, "systemctl.log")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + stubLog + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(stubDir, "systemctl"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write systemctl stub: %v", err)
	}
	env = append(os.Environ(),
		"CP_BASE_URL="+cp,
		"CP_BROKER_URL=tls://iot.test:8883",
		"CP_BOOTSTRAP_KEY_FILE="+keyFile,
		"CP_AGENT_BIN_SRC="+agentBin,
		"CP_AGENT_VERSION=test-0.0.1",
		"CP_ROOT="+root,
		"CP_HARDWARE_KIND=pi",
		"PATH="+stubDir+":"+os.Getenv("PATH"),
	)
	return root, env
}

// runScript exec's install-cp-agent.sh with the given env. Returns
// combined stdout + stderr and the error so the test can inspect both.
func runScript(t *testing.T, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", scriptPath(t))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestInstallScriptCallsEnrollmentWithMachineIDKey is the Issue 22
// tracer: a fresh invocation hits POST /enrollments exactly once,
// carrying Idempotency-Key: <machine-id from /etc/machine-id> and a
// JSON body whose hardware_uuid matches.
func TestInstallScriptCallsEnrollmentWithMachineIDKey(t *testing.T) {
	requireBash(t)
	srv, cap := fakeCP(t)
	_, env := sandboxRoot(t, srv.URL)

	out, err := runScript(t, env)
	if err != nil {
		t.Fatalf("script exited %v\nout:\n%s", err, out)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.enrollHits != 1 {
		t.Errorf("POST /enrollments hits: got %d want 1", cap.enrollHits)
	}
	const wantKey = "11111111111122223333444455556666"
	if cap.idempotencyKey != wantKey {
		t.Errorf("Idempotency-Key: got %q want %q", cap.idempotencyKey, wantKey)
	}
	if cap.body["hardware_uuid"] != wantKey {
		t.Errorf("body.hardware_uuid: got %v want %q", cap.body["hardware_uuid"], wantKey)
	}
	if cap.body["bootstrap_key"] != "test-bootstrap-key-do-not-log" {
		t.Errorf("body.bootstrap_key: got %v", cap.body["bootstrap_key"])
	}
	if cap.body["hardware_kind"] != "pi" {
		t.Errorf("body.hardware_kind: got %v want %q", cap.body["hardware_kind"], "pi")
	}
}

// TestInstallScriptWritesCertAndConfigAt0600 locks cycle 2: after a
// successful enrollment, cert, private key, and agent config live under
// ${CP_ROOT}/etc/uknomi/ with mode 0600 (cert+key) and 0644 (config),
// each carrying the response payload. The CA file lands at 0644 because
// Amazon's root CA is public.
func TestInstallScriptWritesCertAndConfigAt0600(t *testing.T) {
	requireBash(t)
	srv, _ := fakeCP(t)
	root, env := sandboxRoot(t, srv.URL)

	out, err := runScript(t, env)
	if err != nil {
		t.Fatalf("script exited %v\nout:\n%s", err, out)
	}

	checks := []struct {
		path     string
		wantMode os.FileMode
		needle   string
	}{
		{filepath.Join(root, "etc/uknomi/cert.pem"), 0o600, "TESTCERT"},
		{filepath.Join(root, "etc/uknomi/key.pem"), 0o600, "TESTKEY"},
		{filepath.Join(root, "etc/uknomi/agent-config.json"), 0o644, "dev-test-1"},
	}
	for _, c := range checks {
		st, err := os.Stat(c.path)
		if err != nil {
			t.Errorf("stat %s: %v", c.path, err)
			continue
		}
		if st.Mode().Perm() != c.wantMode {
			t.Errorf("%s mode: got %o want %o", c.path, st.Mode().Perm(), c.wantMode)
		}
		data, err := os.ReadFile(c.path)
		if err != nil {
			t.Errorf("read %s: %v", c.path, err)
			continue
		}
		if !contains(string(data), c.needle) {
			t.Errorf("%s missing %q; content=%s", c.path, c.needle, data)
		}
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
