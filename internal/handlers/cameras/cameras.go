// Package cameras implements the agent-side handler for the downward
// cameras.update command (Phase 2 Edge UI rework, issue #2). Wire
// types + validation live in internal/protocol/cameras so the agent
// and CP halves can't drift on what a valid payload is — per ADR-028's
// protective stance.
package cameras

import (
	"context"
	"encoding/json"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// Applier carries out a validated cameras.update. Implementations
// write the list atomically to the agent-managed local cameras file
// and return the effective list back to the handler (typically just
// the input list — included for response symmetry with the slice 2
// config.update handler, where the agent could surface drift).
type Applier interface {
	Apply(ctx context.Context, list []cameras.Camera) ([]cameras.Camera, error)
}

// Response is the handler's success-envelope payload — mirrors the
// shape of the other agent handler responses.
type Response struct {
	AppliedAt        string           `json:"applied_at"`
	EffectiveCameras []cameras.Camera `json:"effective_cameras"`
}

type Handler struct {
	applier Applier
	now     func() time.Time
}

func New(applier Applier) *Handler {
	return &Handler{applier: applier, now: time.Now}
}

// Handle parses + validates the cameras.update payload and applies
// it. Validation failures surface as envelope.CodedError with stable
// codes so CP's audit log + dashboard can render them consistently.
func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	list, err := cameras.ParseUpdateAll(args)
	if err != nil {
		if v, ok := cameras.AsValidation(err); ok {
			return nil, envelope.NewCodedError(v.Code, v.Message)
		}
		return nil, envelope.NewCodedError(cameras.CodeBadPayload, err.Error())
	}
	effective, err := h.applier.Apply(ctx, list)
	if err != nil {
		return nil, envelope.NewCodedError("cameras.apply_failed", err.Error())
	}
	return Response{
		AppliedAt:        h.now().UTC().Format(time.RFC3339),
		EffectiveCameras: effective,
	}, nil
}
