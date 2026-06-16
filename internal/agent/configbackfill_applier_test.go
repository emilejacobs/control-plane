package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/configbackfill"
)

// Apply adds snapshot_state_path while preserving every other config field.
func TestConfigBackfillApplierPreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-config.json")
	if err := os.WriteFile(path, []byte(`{"device_id":"dev-1","broker_url":"tls://x:8883"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewConfigBackfillApplier(path)
	if err := a.Apply(context.Background(), configbackfill.Args{SnapshotStatePath: "/var/uknomi/snapshot-state.json"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("config not valid JSON: %v", err)
	}
	if m["snapshot_state_path"] != "/var/uknomi/snapshot-state.json" {
		t.Errorf("snapshot_state_path: got %v", m["snapshot_state_path"])
	}
	if m["device_id"] != "dev-1" || m["broker_url"] != "tls://x:8883" {
		t.Errorf("existing fields not preserved: %+v", m)
	}
}
