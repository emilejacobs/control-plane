package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/emilejacobs/control-plane/internal/protocol/configbackfill"
)

// ConfigBackfillApplier merges install-time config fields delivered by the
// config.backfill command (#85) into the agent's config file. The affected
// features (e.g. the snapshot scheduler) init at startup, so the change takes
// effect on the agent's next restart — the applier does not hot-reload.
type ConfigBackfillApplier struct {
	path string
}

func NewConfigBackfillApplier(path string) *ConfigBackfillApplier {
	return &ConfigBackfillApplier{path: path}
}

// Apply persists the backfilled fields, preserving every other config field.
func (a *ConfigBackfillApplier) Apply(_ context.Context, args configbackfill.Args) error {
	raw, err := os.ReadFile(a.path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	if args.SnapshotStatePath != "" {
		m["snapshot_state_path"] = args.SnapshotStatePath
	}
	if err := atomicWriteJSON(a.path, m); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
