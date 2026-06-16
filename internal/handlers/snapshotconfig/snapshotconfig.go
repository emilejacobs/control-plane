// Package snapshotconfig implements the agent-side handler for the downward
// snapshot.config command (issue #9): persist the per-device scheduled-snapshot
// cadence to the agent's snapshot state file so the scheduler (slice 3b) reads
// it and it survives a restart.
package snapshotconfig

import (
	"context"
	"encoding/json"

	"github.com/emilejacobs/control-plane/internal/envelope"
	protosnapshotconfig "github.com/emilejacobs/control-plane/internal/protocol/snapshotconfig"
)

// CadenceStore persists the cadence. *snapshotstate.Store satisfies it.
type CadenceStore interface {
	SetCadence(cadence string) error
}

type Handler struct {
	store CadenceStore
}

func New(store CadenceStore) *Handler { return &Handler{store: store} }

// Handle validates + persists the cadence and ACKs it back to CP.
func (h *Handler) Handle(_ context.Context, args json.RawMessage) (any, error) {
	a, err := protosnapshotconfig.ParseArgs(args)
	if err != nil {
		return nil, envelope.NewCodedError("snapshot_config.bad_payload", err.Error())
	}
	if err := h.store.SetCadence(a.Cadence); err != nil {
		return nil, envelope.NewCodedError("snapshot_config.persist_failed", err.Error())
	}
	return struct {
		Cadence string `json:"cadence"`
	}{Cadence: a.Cadence}, nil
}
