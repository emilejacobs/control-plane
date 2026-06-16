// Package configbackfill holds the wire type for the config.backfill command —
// CP → agent (#85). It delivers install-time-only agent-config fields to
// already-enrolled devices whose config predates those fields, so dormant
// features activate without re-provisioning (ADR-036 §6 track 2).
//
// It is deliberately separate from config.update, which ADR-028 keeps to a
// strict two-field hot-reload whitelist. config.backfill persists its fields to
// the agent's config file; because the affected features (e.g. the snapshot
// scheduler) init at startup, the change takes effect on the agent's next
// restart.
package configbackfill

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Args is the config.backfill payload. Fields are optional individually but at
// least one must be present. Add install-time fields here as they need backfill.
type Args struct {
	// SnapshotStatePath enables the scheduled-snapshot scheduler (#9) on a
	// device whose config predates the field.
	SnapshotStatePath string `json:"snapshot_state_path,omitempty"`
}

// IsEmpty reports whether no field was set.
func (a Args) IsEmpty() bool {
	return a.SnapshotStatePath == ""
}

// ParseArgs decodes + validates a config.backfill payload, rejecting unknown
// fields and an empty (no-op) backfill.
func ParseArgs(raw json.RawMessage) (Args, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a Args
	if err := dec.Decode(&a); err != nil {
		return Args{}, fmt.Errorf("decode config.backfill args: %w", err)
	}
	if a.IsEmpty() {
		return Args{}, fmt.Errorf("config.backfill: no fields to backfill")
	}
	return a, nil
}
