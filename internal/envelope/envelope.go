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
	Success       bool            `json:"success"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         *ResultError    `json:"error,omitempty"`
}

type ResultError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
