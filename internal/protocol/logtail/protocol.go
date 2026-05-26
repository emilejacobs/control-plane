// Package logtail holds the wire types and validation helpers for the
// Phase 2 slice 3 log.tail command. Both the agent's dispatcher
// handler and the CP-side POST endpoint depend on this package so the
// two halves can't drift on what a valid request is.
//
// The cmd is the FIFTH unsigned dispatcher handler (per ADR-028's
// blast-radius analysis, widened to cover log-tail: agent only reads
// pre-allow-listed files; worst case an attacker who can publish to
// devices/{id}/cmd exfils a log file the operator could read anyway).
package logtail

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	MinLines       = 1
	MaxLines       = 500
	MaxLogNameLen  = 64
	MaxContentSize = 200 * 1024 // ~200 KB; leaves 50 KB headroom in the 256 KB MQTT envelope
)

// Kind discriminates how the agent fetches the log content for an
// allow-list entry. "file" tails a regular file (slice 3 default);
// "docker" shells out to `docker logs --tail N <container>` (issue #7,
// ADR-030 § 5). Operators only see the logical name + label in the
// dashboard; the resolver picks the executor by kind.
const (
	KindFile   = "file"
	KindDocker = "docker"
)

// Entry is one row in the agent's per-OS log allow-list. Kind chooses
// the executor; Target is the kind-specific identifier (absolute path
// for file, container name for docker). Label is the human-readable
// string the dashboard surfaces in its picker.
type Entry struct {
	Name   string
	Kind   string
	Target string
	Label  string
}

// Error codes returned on the failure path. Stable strings — the
// agent's cmd-result envelope carries them back to CP and on to the
// dashboard's error rendering.
const (
	CodeBadPayload       = "log_tail.bad_payload"
	CodeBadLogName       = "log_tail.bad_log_name"
	CodeBadLines         = "log_tail.bad_lines"
	CodeUnknownLog       = "log_tail.unknown_log"        // log_name not in agent allow-list
	CodeBinaryFile       = "log_tail.binary_file"        // agent heuristic rejected
	CodeReadError        = "log_tail.read_error"         // file exists but couldn't read
	CodeUnknownField     = "log_tail.unknown_field"
)

// ValidationError carries a stable Code + human Message. Callers wrap
// it in whatever envelope is appropriate for their boundary (agent
// uses envelope.CodedError; API translates to HTTP 400 body).
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

// Request is the on-wire shape both the API body and the cmd's Args
// field carry. log_name is the logical name (e.g. "agent",
// "webui-error") — the agent resolves it to the actual file path.
type Request struct {
	LogName string `json:"log_name"`
	Lines   int    `json:"lines"`
}

// Response is the success-path agent → cp shape sent in
// envelope.Result.Result. Truncated/TruncatedFrom report when the
// agent had to cap the response to fit MQTT.
type Response struct {
	Content       string `json:"content"`
	Truncated     bool   `json:"truncated"`
	TruncatedFrom int    `json:"truncated_from,omitempty"` // lines the agent asked for before capping
}

// Parse + validate the raw JSON payload. Used by both the agent
// dispatcher (validates cmd.Args before reading the file) and the CP
// API (validates the request body before persisting + publishing).
// Returns the parsed Request on success.
func Parse(raw json.RawMessage) (Request, error) {
	if err := rejectUnknownFields(raw); err != nil {
		return Request{}, err
	}
	var req Request
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return Request{}, newErr(CodeBadPayload, err.Error())
		}
	}
	if req.LogName == "" || len(req.LogName) > MaxLogNameLen {
		return Request{}, newErr(CodeBadLogName,
			fmt.Sprintf("log_name length must be 1..%d, got %d", MaxLogNameLen, len(req.LogName)))
	}
	if req.Lines < MinLines || req.Lines > MaxLines {
		return Request{}, newErr(CodeBadLines,
			fmt.Sprintf("lines must be %d..%d, got %d", MinLines, MaxLines, req.Lines))
	}
	return req, nil
}

// rejectUnknownFields enforces the strict two-field whitelist.
func rejectUnknownFields(raw json.RawMessage) error {
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
