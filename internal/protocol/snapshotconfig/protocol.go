// Package snapshotconfig holds the wire type for the snapshot.config command
// (issue #9, ADR-030 § 7) — CP → agent, the per-device scheduled-snapshot
// cadence. It's a dedicated command rather than another config.update field
// because config.update is intentionally a strict two-field whitelist (ADR-028)
// and its applier hot-reloads the service-status reporters; the snapshot
// scheduler is unrelated and persists to its own state file.
//
// Shared by cp-api (builds Args from the snapshot-config PUT) and the agent
// handler (consumes Args), so the two can't drift on valid cadences.
package snapshotconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Cadences is the closed set of valid cadences, mirroring the
// devices.snapshot_cadence CHECK (migration 025).
var Cadences = map[string]bool{"off": true, "daily": true, "weekly": true}

// ValidCadence reports whether c is an accepted cadence.
func ValidCadence(c string) bool { return Cadences[c] }

// Args is the snapshot.config command payload.
type Args struct {
	Cadence string `json:"cadence"`
}

// ParseArgs decodes + validates a snapshot.config payload, rejecting unknown
// fields and any cadence outside the closed set.
func ParseArgs(raw json.RawMessage) (Args, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a Args
	if err := dec.Decode(&a); err != nil {
		return Args{}, fmt.Errorf("decode snapshot.config args: %w", err)
	}
	if !ValidCadence(a.Cadence) {
		return Args{}, fmt.Errorf("snapshot.config: invalid cadence %q (want off|daily|weekly)", a.Cadence)
	}
	return a, nil
}
