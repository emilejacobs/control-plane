// Package installscript_test also drives the macOS supervisor-migration
// script (scripts/migrate-cp-agent-supervisor.sh — issue #39). The fleet's
// ~63 Macs were installed on the old layout, where the com.uknomi.agent
// LaunchDaemon runs the agent binary DIRECTLY. This script converts an
// already-installed Mac to the resident-wrapper layout (ADR-035 §3) WITHOUT
// re-enrolling: it lays out AGENT_DIR/current, installs the supervisor as the
// LaunchDaemon Program, and rewrites the plist to run the supervisor with
// AGENT_DIR/AGENT_ARGS. It mirrors the bench machine's hand conversion.
//
// launchctl is stubbed on PATH (records its argv) so the test needs no real
// launchd; the path that fires on a fleet Mac is the same path under test.
package installscript_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// migrateScriptPath returns the absolute path to
// migrate-cp-agent-supervisor.sh, computed from the test binary's location.
func migrateScriptPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repo := filepath.Clean(filepath.Join(wd, "..", ".."))
	return filepath.Join(repo, "scripts", "migrate-cp-agent-supervisor.sh")
}

// macSandbox lays out a per-test fake macOS root under t.TempDir() that
// already carries an OLD-layout install: enrolled cert/key/config under
// /var/uknomi, the agent binary at /usr/local/bin/uknomi-agent, and a
// direct-exec com.uknomi.agent plist. The migration script targets it via
// CP_ROOT. Returns the root and an env slice for exec.Cmd.Env. The fresh
// (version-stamped) agent binary the script installs as `current` carries a
// distinct sentinel so the test can prove the staged binary — not the old
// one — became current.
func macSandbox(t *testing.T) (root string, env []string) {
	t.Helper()
	root = t.TempDir()

	// ── Existing old-layout install ──────────────────────────────────────
	runtimeDir := filepath.Join(root, "var", "uknomi")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir var/uknomi: %v", err)
	}
	for name, content := range map[string]string{
		"cert.pem":          "-----BEGIN CERTIFICATE-----\nOLDCERT\n-----END CERTIFICATE-----\n",
		"key.pem":           "-----BEGIN PRIVATE KEY-----\nOLDKEY\n-----END PRIVATE KEY-----\n",
		"ca.pem":            "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n",
		"agent-config.json": `{"device_id":"dev-mac-1","version":"old-0.0.1"}` + "\n",
	} {
		mode := os.FileMode(0o600)
		if name == "ca.pem" || name == "agent-config.json" {
			mode = 0o644
		}
		if err := os.WriteFile(filepath.Join(runtimeDir, name), []byte(content), mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// Old-layout agent binary installed directly as the daemon Program.
	binDir := filepath.Join(root, "usr", "local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir usr/local/bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "uknomi-agent"),
		[]byte("#!/bin/sh\necho OLD-UNSTAMPED-AGENT\n"), 0o755); err != nil {
		t.Fatalf("write old agent: %v", err)
	}

	// Old-layout plist: ProgramArguments runs the binary directly.
	daemonDir := filepath.Join(root, "Library", "LaunchDaemons")
	if err := os.MkdirAll(daemonDir, 0o755); err != nil {
		t.Fatalf("mkdir LaunchDaemons: %v", err)
	}
	oldPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>com.uknomi.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/uknomi-agent</string>
        <string>--config</string>
        <string>/var/uknomi/agent-config.json</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(daemonDir, "com.uknomi.agent.plist"),
		[]byte(oldPlist), 0o644); err != nil {
		t.Fatalf("write old plist: %v", err)
	}

	// ── Package-bundled sources the migration installs ───────────────────
	// A FRESH version-stamped agent binary (distinct sentinel) and the
	// supervisor. On a fleet Mac these ship inside the install package.
	freshBin := filepath.Join(root, "pkg-uknomi-agent")
	if err := os.WriteFile(freshBin, []byte("#!/bin/sh\necho FRESH-STAMPED-1.4.0\n"), 0o755); err != nil {
		t.Fatalf("write fresh agent: %v", err)
	}
	supervisorSrc := filepath.Join(root, "pkg-uknomi-agent-supervisor.sh")
	if err := os.WriteFile(supervisorSrc,
		[]byte("#!/bin/sh\nexec \"$AGENT_DIR/current\" $AGENT_ARGS\n"), 0o755); err != nil {
		t.Fatalf("write supervisor: %v", err)
	}

	// ── launchctl stub on PATH ───────────────────────────────────────────
	stubDir := filepath.Join(root, "stubs")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatalf("mkdir stubs: %v", err)
	}
	stubLog := filepath.Join(root, "launchctl.log")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + stubLog + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(stubDir, "launchctl"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write launchctl stub: %v", err)
	}

	env = append(os.Environ(),
		"CP_ROOT="+root,
		"CP_AGENT_BIN_SRC="+freshBin,
		"CP_SUPERVISOR_SRC="+supervisorSrc,
		"PATH="+stubDir+":"+os.Getenv("PATH"),
	)
	return root, env
}

// TestMigratePreservesEnrollment locks the core safety property: migration
// must NOT re-enroll. The device's cert, private key, CA, and agent-config
// under /var/uknomi are left byte-for-byte untouched — a migration that
// rewrote them would orphan the device's IoT identity across the whole fleet.
func TestMigratePreservesEnrollment(t *testing.T) {
	requireBash(t)
	root, env := macSandbox(t)

	runtimeDir := filepath.Join(root, "var", "uknomi")
	before := map[string][]byte{}
	for _, name := range []string{"cert.pem", "key.pem", "ca.pem", "agent-config.json"} {
		b, err := os.ReadFile(filepath.Join(runtimeDir, name))
		if err != nil {
			t.Fatalf("read %s before: %v", name, err)
		}
		before[name] = b
	}

	out, err := runMigrate(t, env)
	if err != nil {
		t.Fatalf("migrate exited %v\nout:\n%s", err, out)
	}

	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(runtimeDir, name))
		if err != nil {
			t.Fatalf("read %s after: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s was modified by migration\nbefore: %s\nafter:  %s", name, want, got)
		}
	}
}

// TestMigrateRefusesWhenNotEnrolled locks the guard: on a Mac with no
// agent-config (never enrolled), migration must refuse with a non-zero exit
// rather than lay down a crash-looping wrapper that has no device identity.
func TestMigrateRefusesWhenNotEnrolled(t *testing.T) {
	requireBash(t)
	root, env := macSandbox(t)

	// Remove the enrollment marker.
	if err := os.Remove(filepath.Join(root, "var", "uknomi", "agent-config.json")); err != nil {
		t.Fatalf("rm agent-config: %v", err)
	}

	out, err := runMigrate(t, env)
	if err == nil {
		t.Fatalf("migration succeeded on an unenrolled Mac; want non-zero exit\nout:\n%s", out)
	}
	// And it must not have laid down the wrapper layout.
	if _, statErr := os.Stat(filepath.Join(root, "var/uknomi/agent-update/current")); statErr == nil {
		t.Errorf("migration laid out AGENT_DIR/current despite refusing")
	}
}

// TestMigrateIsIdempotent locks re-runnability: running the migration twice
// converges on the same state and stays green — the operator can safely
// re-run it across the fleet (or after a transient launchctl hiccup).
func TestMigrateIsIdempotent(t *testing.T) {
	requireBash(t)
	root, env := macSandbox(t)

	for i := 1; i <= 2; i++ {
		out, err := runMigrate(t, env)
		if err != nil {
			t.Fatalf("migrate run %d failed %v\nout:\n%s", i, err, out)
		}
	}

	cur, err := os.ReadFile(filepath.Join(root, "var/uknomi/agent-update/current"))
	if err != nil {
		t.Fatalf("current missing after two runs: %v", err)
	}
	if !contains(string(cur), "FRESH-STAMPED-1.4.0") {
		t.Errorf("current is not the fresh stamped binary after two runs; content=%s", cur)
	}
}

// TestMigrateScriptIsShellcheckClean keeps the migration script to the same
// shellcheck bar as the Linux installer. Skips locally without shellcheck; CI
// enforces it.
func TestMigrateScriptIsShellcheckClean(t *testing.T) {
	requireBash(t)
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not in PATH; CI is expected to enforce this")
	}
	cmd := exec.Command("shellcheck", "--severity=warning", migrateScriptPath(t))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shellcheck reported issues (severity>=warning):\n%s", out)
	}
}

// runMigrate exec's migrate-cp-agent-supervisor.sh with the given env.
func runMigrate(t *testing.T, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", migrateScriptPath(t))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestMigrateLaysOutResidentWrapper is the tracer: after migrating an
// already-installed Mac, the resident-wrapper layout is in place — the
// FRESH stamped binary is AGENT_DIR/current, the supervisor is the daemon
// Program, and the plist runs the supervisor with AGENT_DIR/AGENT_ARGS.
func TestMigrateLaysOutResidentWrapper(t *testing.T) {
	requireBash(t)
	root, env := macSandbox(t)

	out, err := runMigrate(t, env)
	if err != nil {
		t.Fatalf("migrate exited %v\nout:\n%s", err, out)
	}

	// current = the fresh stamped binary, executable.
	currentPath := filepath.Join(root, "var/uknomi/agent-update/current")
	cur, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("current not installed at %s: %v", currentPath, err)
	}
	if !contains(string(cur), "FRESH-STAMPED-1.4.0") {
		t.Errorf("current is not the fresh stamped binary; content=%s", cur)
	}
	if st, _ := os.Stat(currentPath); st != nil && st.Mode().Perm()&0o111 == 0 {
		t.Errorf("current is not executable; mode=%o", st.Mode().Perm())
	}

	// Supervisor installed as the daemon Program.
	supPath := filepath.Join(root, "usr/local/bin/uknomi-agent-supervisor")
	if st, err := os.Stat(supPath); err != nil {
		t.Errorf("supervisor not installed at %s: %v", supPath, err)
	} else if st.Mode().Perm()&0o111 == 0 {
		t.Errorf("supervisor not executable; mode=%o", st.Mode().Perm())
	}

	// Plist rewritten to run the supervisor with AGENT_DIR/AGENT_ARGS.
	plistPath := filepath.Join(root, "Library/LaunchDaemons/com.uknomi.agent.plist")
	plist, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	for _, needle := range []string{
		"/usr/local/bin/uknomi-agent-supervisor",
		"<key>EnvironmentVariables</key>",
		"<key>AGENT_DIR</key>",
		"<string>/var/uknomi/agent-update</string>",
		"<key>AGENT_ARGS</key>",
		"<string>--config /var/uknomi/agent-config.json</string>",
	} {
		if !contains(string(plist), needle) {
			t.Errorf("plist missing %q; content=\n%s", needle, plist)
		}
	}

	// launchctl reloaded the daemon: unload then load.
	logBytes, err := os.ReadFile(filepath.Join(root, "launchctl.log"))
	if err != nil {
		t.Fatalf("read launchctl log: %v", err)
	}
	for _, want := range []string{"unload", "load"} {
		if !contains(string(logBytes), want) {
			t.Errorf("launchctl never called with %q; log=\n%s", want, logBytes)
		}
	}
}
