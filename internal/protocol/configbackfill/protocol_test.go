package configbackfill_test

import (
	"encoding/json"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/configbackfill"
)

func TestParseArgsSnapshotStatePath(t *testing.T) {
	a, err := configbackfill.ParseArgs(json.RawMessage(`{"snapshot_state_path":"/var/uknomi/snapshot-state.json"}`))
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if a.SnapshotStatePath != "/var/uknomi/snapshot-state.json" {
		t.Errorf("path: got %q", a.SnapshotStatePath)
	}
}

func TestParseArgsRejectsUnknownFields(t *testing.T) {
	if _, err := configbackfill.ParseArgs(json.RawMessage(`{"bogus":true}`)); err == nil {
		t.Fatal("expected error on unknown field")
	}
}

// An empty payload (no fields to backfill) is rejected — a no-op backfill is a
// caller mistake, not a silent success.
func TestParseArgsRejectsEmpty(t *testing.T) {
	if _, err := configbackfill.ParseArgs(json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error on empty backfill")
	}
}
