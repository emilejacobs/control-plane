// Package commission is the agent-side handler for the commission command
// (#91, ADR-036). It brings an assigned device into service: join the tailnet
// with the minted key, and — for ALPR devices — start the Plate Recognizer
// container with the license + token. The secret-handling work is delegated to
// an injected Applier so the dispatch logic stays testable.
package commission

import (
	"context"
	"encoding/json"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	commissionproto "github.com/emilejacobs/control-plane/internal/protocol/commission"
)

// Applier performs the device-side commission actions. The real implementation
// shells out to `tailscale up` and drives the Colima container through the
// per-user runner (#89).
type Applier interface {
	JoinTailnet(ctx context.Context, authKey string) error
	StartALPR(ctx context.Context, license, token string) error
}

// Handler dispatches the commission command.
type Handler struct {
	applier Applier
	now     func() time.Time
}

// New returns a commission handler over applier.
func New(applier Applier) *Handler {
	return &Handler{applier: applier, now: time.Now}
}

// Response is the cmd-result payload the CP records on ACK.
type Response struct {
	AppliedAt       string `json:"applied_at"`
	TailscaleJoined bool   `json:"tailscale_joined"`
	ALPRStarted     bool   `json:"alpr_started"`
}

// Handle parses + applies a commission command.
func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	a, err := commissionproto.ParseArgs(args)
	if err != nil {
		return nil, envelope.NewCodedError("commission.bad_payload", err.Error())
	}

	if err := h.applier.JoinTailnet(ctx, a.TailscaleAuthKey); err != nil {
		return nil, envelope.NewCodedError("commission.tailscale_join_failed", err.Error())
	}

	alprStarted := false
	if a.ALPR != nil {
		if err := h.applier.StartALPR(ctx, a.ALPR.License, a.ALPR.Token); err != nil {
			return nil, envelope.NewCodedError("commission.alpr_failed", err.Error())
		}
		alprStarted = true
	}

	return Response{
		AppliedAt:       h.now().UTC().Format(time.RFC3339),
		TailscaleJoined: true,
		ALPRStarted:     alprStarted,
	}, nil
}
