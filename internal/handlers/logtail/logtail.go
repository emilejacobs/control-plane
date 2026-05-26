// Package logtail implements the agent-side handler for the downward
// `log.tail` command (Phase 2 slice 3). Wire types and validation
// rules live in internal/protocol/logtail so the agent and CP halves
// can't drift.
//
// Path resolution is gated by the per-OS allow-list (Reader.AllowList)
// — a log_name that isn't in the map returns CodeUnknownLog without
// ever calling the file reader. This is the security boundary that
// makes the unsigned handler safe to ship in Phase 2 (per ADR-028
// extended to cover log.tail).
package logtail

import (
	"context"
	"encoding/json"

	"github.com/emilejacobs/control-plane/internal/envelope"
	protologtail "github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

// Reader is the log-fetch side of the agent the handler depends on.
// AllowList returns the logical-name → Entry map (issue #7); Tail
// fetches the last N lines using the Entry's Kind to pick the
// executor. The agent wires the real implementation (PerOSAllowList +
// kind-aware fetch in the agent package); tests pass a stub.
type Reader interface {
	AllowList() map[string]protologtail.Entry
	Tail(entry protologtail.Entry, lines int) (protologtail.Response, error)
}

type Handler struct {
	reader Reader
}

func New(reader Reader) *Handler {
	return &Handler{reader: reader}
}

func (h *Handler) Handle(_ context.Context, args json.RawMessage) (any, error) {
	req, err := protologtail.Parse(args)
	if err != nil {
		return nil, asCodedError(err)
	}

	entry, ok := h.reader.AllowList()[req.LogName]
	if !ok {
		return nil, envelope.NewCodedError(protologtail.CodeUnknownLog,
			"log_name "+req.LogName+" is not in this agent's allow-list")
	}

	resp, err := h.reader.Tail(entry, req.Lines)
	if err != nil {
		return nil, asCodedError(err)
	}
	return resp, nil
}

func asCodedError(err error) error {
	if v, ok := protologtail.AsValidation(err); ok {
		return envelope.NewCodedError(v.Code, v.Message)
	}
	return envelope.NewCodedError(protologtail.CodeReadError, err.Error())
}
