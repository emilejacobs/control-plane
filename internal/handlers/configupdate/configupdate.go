// Package configupdate implements the agent-side handler for the
// downward `config.update` command (Phase 2 slice 2). Wire types and
// validation rules live in internal/protocol/configupdate so the
// agent and CP halves can't drift on what a valid payload is — per
// ADR-028's strict two-field whitelist.
package configupdate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/configupdate"
)

// Applier carries out a validated config.update. allowList and interval
// are pointers so the handler can pass through three distinct shapes:
// nil = "leave / clear this override", &[]string{x} = "set this list",
// &[]string{} = "set empty list (track nothing)". Implementations
// persist to disk + hot-reload publisher state; returns the effective
// state for the response envelope.
type Applier interface {
	Apply(ctx context.Context, allowList *[]string, interval *time.Duration) ([]string, time.Duration, error)
}

// Response is the handler's success-envelope payload. Mirrors the
// agent's other handler-response shapes (servicestatus.Response, etc.).
type Response struct {
	AppliedAt          string   `json:"applied_at"`
	EffectiveAllowList []string `json:"effective_allow_list"`
	EffectiveInterval  string   `json:"effective_interval"`
}

type Handler struct {
	applier Applier
	now     func() time.Time
}

func New(applier Applier) *Handler {
	return &Handler{applier: applier, now: time.Now}
}

func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	allowList, interval, err := configupdate.Parse(args)
	if err != nil {
		if v, ok := configupdate.AsValidation(err); ok {
			return nil, envelope.NewCodedError(v.Code, v.Message)
		}
		return nil, envelope.NewCodedError(configupdate.CodeBadPayload, err.Error())
	}

	effList, effInterval, err := h.applier.Apply(ctx, allowList, interval)
	if err != nil {
		return nil, envelope.NewCodedError("config_update.apply_failed", err.Error())
	}

	return Response{
		AppliedAt:          h.now().UTC().Format(time.RFC3339),
		EffectiveAllowList: effList,
		EffectiveInterval:  effInterval.String(),
	}, nil
}
