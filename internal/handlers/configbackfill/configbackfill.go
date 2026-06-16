// Package configbackfill is the agent-side handler for the config.backfill
// command (#85): persist install-time config fields delivered by the CP to the
// agent's config file. Takes effect on the agent's next restart.
package configbackfill

import (
	"context"
	"encoding/json"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	proto "github.com/emilejacobs/control-plane/internal/protocol/configbackfill"
)

// Applier persists the backfilled fields.
type Applier interface {
	Apply(ctx context.Context, args proto.Args) error
}

// Handler dispatches config.backfill.
type Handler struct {
	applier Applier
	now     func() time.Time
}

func New(applier Applier) *Handler { return &Handler{applier: applier, now: time.Now} }

// Response is the cmd-result payload.
type Response struct {
	AppliedAt            string `json:"applied_at"`
	TakesEffectOnRestart bool   `json:"takes_effect_on_restart"`
}

func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	a, err := proto.ParseArgs(args)
	if err != nil {
		return nil, envelope.NewCodedError("config_backfill.bad_payload", err.Error())
	}
	if err := h.applier.Apply(ctx, a); err != nil {
		return nil, envelope.NewCodedError("config_backfill.apply_failed", err.Error())
	}
	return Response{AppliedAt: h.now().UTC().Format(time.RFC3339), TakesEffectOnRestart: true}, nil
}
