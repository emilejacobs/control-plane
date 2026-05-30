// Package agentsupervisor_test drives scripts/uknomi-agent-supervisor.sh (the
// resident wrapper, #39) through its promote / rollback decision with fake
// candidate binaries, asserting the on-disk outcome. Uses SUPERVISOR_GATE_ONLY
// so the script resolves the candidate and exits instead of exec'ing an agent.
package agentsupervisor_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const supervisor = "../../scripts/uknomi-agent-supervisor.sh"

// goodCandidate writes the health marker (= candidate.version) then idles —
// i.e. proves itself alive+controllable. badCandidate idles without the marker.
const goodCandidate = "#!/bin/sh\ncat \"$AGENT_DIR/candidate.version\" > \"$AGENT_DIR/healthy\"\nexec sleep 10\n"
const badCandidate = "#!/bin/sh\nexec sleep 10\n"

func setupDir(t *testing.T, candidateScript string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("current", "#!/bin/sh\nsleep 30\n# OLD-CURRENT\n", 0o755)
	write("candidate", candidateScript, 0o755)
	write("candidate.version", "1.4.0", 0o644)
	write("trying", "1.4.0", 0o644)
	return dir
}

func runGate(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("sh", supervisor)
	cmd.Env = append(os.Environ(),
		"AGENT_DIR="+dir,
		"SUPERVISOR_GATE_ONLY=1",
		"HEALTH_TIMEOUT=4",
		"HEALTH_POLL=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("supervisor gate: %v\n%s", err, out)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// TestPromotesHealthyCandidate — a candidate that reports healthy in time is
// promoted to current, the old current is kept as last-known-good, and the
// trying flag is cleared with no rollback recorded.
func TestPromotesHealthyCandidate(t *testing.T) {
	dir := setupDir(t, goodCandidate)
	runGate(t, dir)

	if read(t, dir, "current") != goodCandidate {
		t.Errorf("current was not replaced with the candidate")
	}
	if lkg := read(t, dir, "last-known-good"); lkg == "" || lkg == goodCandidate {
		t.Errorf("last-known-good = %q, want the prior current", lkg)
	}
	if exists(dir, "trying") || exists(dir, "candidate") {
		t.Errorf("trying/candidate not cleared after promote")
	}
	if exists(dir, "rolled-back") {
		t.Errorf("rollback recorded on a healthy promote")
	}
}

// TestRollsBackUnhealthyCandidate — a candidate that never reports healthy is
// discarded: current is unchanged, the candidate is removed, and a rollback is
// recorded.
func TestRollsBackUnhealthyCandidate(t *testing.T) {
	dir := setupDir(t, badCandidate)
	runGate(t, dir)

	if cur := read(t, dir, "current"); cur == badCandidate {
		t.Errorf("current was replaced by the unhealthy candidate")
	}
	if exists(dir, "trying") || exists(dir, "candidate") {
		t.Errorf("trying/candidate not cleared after rollback")
	}
	if !exists(dir, "rolled-back") {
		t.Errorf("no rollback recorded for an unhealthy candidate")
	}
	if log := read(t, dir, "rollback.log"); log == "" {
		t.Errorf("rollback.log empty; want the rolled-back version")
	}
}
