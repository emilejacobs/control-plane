// Package prconfigupdate implements the agent-side handler for the downward
// pr.config.update command (issue #5, ADR-030 §3). It validates the payload and
// hands off to an Applier that merges the CP-managed fields into the on-disk
// config.ini and restarts the Plate Recognizer container. Wire types live in
// internal/protocol/prconfig so the CP and agent halves can't drift.
package prconfigupdate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

// Applier merges the validated config into the device's config.ini and bounces
// the container. Real impl lives in internal/agent; tests use a fake.
type Applier interface {
	Apply(ctx context.Context, req prconfig.UpdateRequest) error
}

// Response is the success-envelope payload.
type Response struct {
	AppliedAt string `json:"applied_at"`
	Restarted bool   `json:"restarted"`
}

type Handler struct {
	applier Applier
	now     func() time.Time
}

func New(applier Applier) *Handler {
	return &Handler{applier: applier, now: time.Now}
}

func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var req prconfig.UpdateRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, envelope.NewCodedError("pr.config.bad_payload", err.Error())
	}
	if err := prconfig.Validate(req.Config); err != nil {
		return nil, envelope.NewCodedError("pr.config.invalid", err.Error())
	}
	if err := h.applier.Apply(ctx, req); err != nil {
		return nil, envelope.NewCodedError("pr.config.apply_failed", err.Error())
	}
	return Response{AppliedAt: h.now().UTC().Format(time.RFC3339), Restarted: true}, nil
}
