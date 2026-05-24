// Package configupdate holds the wire types and validation helpers for
// the Phase 2 slice 2 downward config.update command. Both the agent's
// dispatcher handler and the CP-side API endpoint depend on this
// package so the two halves can't drift on what a valid payload is —
// per ADR-028's strict two-field whitelist.
package configupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	MinInterval = 30 * time.Second
	MaxInterval = time.Hour
	MaxNameLen  = 256
)

// Error codes returned by the parsers. Stable strings — the agent's
// cmd-result envelope carries them back to CP, where they end up in
// the audit log and (eventually) on the dashboard.
const (
	CodeBadPayload     = "config_update.bad_payload"
	CodeBadInterval    = "config_update.bad_interval"
	CodeBadServiceName = "config_update.bad_service_name"
	CodeUnknownField   = "config_update.unknown_field"
)

// ValidationError carries a stable Code + human Message. Callers wrap
// it in whatever envelope is appropriate for their boundary (the agent
// uses envelope.CodedError; the API translates to an HTTP 400 body).
type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func newErr(code, msg string) error { return &ValidationError{Code: code, Message: msg} }

// AsValidation extracts a *ValidationError from err if present.
func AsValidation(err error) (*ValidationError, bool) {
	var v *ValidationError
	if errors.As(err, &v) {
		return v, true
	}
	return nil, false
}

// Request mirrors the on-wire payload. RawMessage preserves the
// presence-vs-null distinction that json:",omitempty" loses — needed
// because nil / [] / set are three meaningful states.
type Request struct {
	ServiceAllowList      json.RawMessage `json:"service_allow_list,omitempty"`
	ServiceStatusInterval json.RawMessage `json:"service_status_interval,omitempty"`
}

// Parse runs the full validation pass on a raw JSON payload and
// returns the parsed pointer-pair the Applier (or the registry write)
// will consume. Unknown fields → CodeUnknownField. Field-shape errors
// → CodeBadPayload. Interval / name errors → CodeBadInterval /
// CodeBadServiceName.
func Parse(raw json.RawMessage) (allowList *[]string, interval *time.Duration, err error) {
	if err := RejectUnknownFields(raw); err != nil {
		return nil, nil, err
	}
	var req Request
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, nil, newErr(CodeBadPayload, err.Error())
		}
	}
	allowList, err = ParseAllowList(req.ServiceAllowList)
	if err != nil {
		return nil, nil, err
	}
	interval, err = ParseInterval(req.ServiceStatusInterval)
	if err != nil {
		return nil, nil, err
	}
	return allowList, interval, nil
}

// RejectUnknownFields enforces ADR-028's strict whitelist.
func RejectUnknownFields(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var probe Request
	if err := dec.Decode(&probe); err != nil {
		return newErr(CodeUnknownField, err.Error())
	}
	return nil
}

// ParseAllowList accepts a raw service_allow_list field. nil / absent
// / JSON null → nil pointer (caller's "leave alone / clear" semantics).
// Empty JSON array → non-nil empty slice ("track nothing").
func ParseAllowList(raw json.RawMessage) (*[]string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, newErr(CodeBadPayload, fmt.Sprintf("service_allow_list: %v", err))
	}
	if list == nil {
		return nil, nil
	}
	for _, name := range list {
		if len(name) == 0 || len(name) > MaxNameLen {
			return nil, newErr(CodeBadServiceName,
				fmt.Sprintf("service name length must be 1..%d, got %d", MaxNameLen, len(name)))
		}
	}
	out := list
	return &out, nil
}

// ParseInterval accepts a raw service_status_interval field. nil /
// absent / JSON null → nil pointer. Otherwise must be a
// time.ParseDuration string within [MinInterval, MaxInterval].
func ParseInterval(raw json.RawMessage) (*time.Duration, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, newErr(CodeBadPayload, fmt.Sprintf("service_status_interval: %v", err))
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, newErr(CodeBadInterval, err.Error())
	}
	if d < MinInterval || d > MaxInterval {
		return nil, newErr(CodeBadInterval,
			fmt.Sprintf("interval %s outside %s..%s", d, MinInterval, MaxInterval))
	}
	return &d, nil
}
