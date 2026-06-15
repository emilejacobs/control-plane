package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// rolledBackVersion reads the most recent reverted version from
// <UpdateDir>/rollback.log (the resident wrapper appends one line per
// rollback). White-box because the heartbeat collector that consumes it is
// unexported.
func TestRolledBackVersion(t *testing.T) {
	dir := t.TempDir()

	// No log file yet → nothing to report.
	a := &Agent{updateDir: dir}
	if v := a.rolledBackVersion(); v != "" {
		t.Errorf("no rollback.log: got %q want empty", v)
	}

	// Multiple rollbacks recorded — the LAST (most recent) wins, trailing
	// whitespace trimmed.
	if err := os.WriteFile(filepath.Join(dir, "rollback.log"), []byte("1.4.0\n1.4.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := a.rolledBackVersion(); v != "1.4.1" {
		t.Errorf("got %q want 1.4.1", v)
	}

	// Not running under the resident wrapper (no UpdateDir) → empty, no read.
	if v := (&Agent{updateDir: ""}).rolledBackVersion(); v != "" {
		t.Errorf("no UpdateDir: got %q want empty", v)
	}
}
