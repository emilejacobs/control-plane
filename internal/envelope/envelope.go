package envelope

import (
	"encoding/json"
	"time"
)

type Command struct {
	CorrelationID string          `json:"correlation_id"`
	CommandID     string          `json:"command_id"`
	Type          string          `json:"type"`
	Args          json.RawMessage `json:"args,omitempty"`
	IssuedAt      time.Time       `json:"issued_at"`
	ExpiresAt     *time.Time      `json:"expires_at,omitempty"`
	Signature     *string         `json:"signature"`
}

type Result struct {
	CorrelationID string          `json:"correlation_id"`
	CommandID     string          `json:"command_id"`
	// Type echoes the originating Command.Type so cp-side consumers
	// can route an ACK to the right per-type handler without keeping
	// an in-memory map of pending commands. Empty when produced by
	// pre-Phase-2-slice-2 agents (handler treats those as ignorable).
	Type    string          `json:"type,omitempty"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResultError    `json:"error,omitempty"`
}

type ResultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CodedError is an error a handler can return to surface a stable, machine-readable
// code on the result envelope (e.g. "service.not_found"). The dispatcher unwraps it
// via errors.As; any handler error that does not implement this contract falls back
// to "handler.error".
type CodedError struct {
	Code    string
	Message string
}

func NewCodedError(code, message string) *CodedError {
	return &CodedError{Code: code, Message: message}
}

func (e *CodedError) Error() string { return e.Message }
