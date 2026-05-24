// Package configupdate implements the agent-side handler for the
// downward `config.update` command (Phase 2 slice 2). The handler
// validates a strict two-field payload (service_allow_list +
// service_status_interval) — per ADR-028's blast-radius cap — and
// delegates application to an Applier the agent wires in. Validation
// rejects unknown fields so any drift from the whitelist is loud.
package configupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
)

const (
	minInterval = 30 * time.Second
	maxInterval = time.Hour
	maxNameLen  = 256
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

// request mirrors the payload schema. json.RawMessage fields preserve
// the presence-vs-null distinction that json:",omitempty" loses.
type request struct {
	ServiceAllowList      json.RawMessage `json:"service_allow_list,omitempty"`
	ServiceStatusInterval json.RawMessage `json:"service_status_interval,omitempty"`
}

func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	if err := rejectUnknownFields(args); err != nil {
		return nil, err
	}

	var req request
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, envelope.NewCodedError("config_update.bad_payload", err.Error())
		}
	}

	allowList, err := parseAllowList(req.ServiceAllowList)
	if err != nil {
		return nil, err
	}
	interval, err := parseInterval(req.ServiceStatusInterval)
	if err != nil {
		return nil, err
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

// rejectUnknownFields enforces ADR-028's strict whitelist: only
// service_allow_list and service_status_interval are accepted. Extra
// keys are a strong signal the cp-side has drifted; failing loud
// catches it at the agent boundary rather than letting silent drops
// mislead the operator.
func rejectUnknownFields(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var probe struct {
		ServiceAllowList      json.RawMessage `json:"service_allow_list"`
		ServiceStatusInterval json.RawMessage `json:"service_status_interval"`
	}
	if err := dec.Decode(&probe); err != nil {
		return envelope.NewCodedError("config_update.unknown_field", err.Error())
	}
	return nil
}

func parseAllowList(raw json.RawMessage) (*[]string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, envelope.NewCodedError("config_update.bad_payload",
			fmt.Sprintf("service_allow_list: %v", err))
	}
	if list == nil {
		// JSON [] decodes to a non-nil empty slice; nil here means the
		// JSON value was effectively absent — treat as clear.
		return nil, nil
	}
	for _, name := range list {
		if len(name) == 0 || len(name) > maxNameLen {
			return nil, envelope.NewCodedError("config_update.bad_service_name",
				fmt.Sprintf("service name length must be 1..%d, got %d", maxNameLen, len(name)))
		}
	}
	out := list
	return &out, nil
}

func parseInterval(raw json.RawMessage) (*time.Duration, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, envelope.NewCodedError("config_update.bad_payload",
			fmt.Sprintf("service_status_interval: %v", err))
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, envelope.NewCodedError("config_update.bad_interval", err.Error())
	}
	if d < minInterval || d > maxInterval {
		return nil, envelope.NewCodedError("config_update.bad_interval",
			fmt.Sprintf("interval %s outside %s..%s", d, minInterval, maxInterval))
	}
	return &d, nil
}
