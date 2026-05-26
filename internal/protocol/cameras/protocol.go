// Package cameras holds the wire types and validation for the
// cameras inventory surface introduced by Phase 2's Edge UI rework
// (ADR-030 § 1). Both the CP-side API handlers and the agent-side
// cameras.update handler depend on this package so the two halves
// can't drift on what a valid camera record looks like.
package cameras

import (
	"errors"
	"strings"
)

// Camera is the wire shape for one camera row — used in both the API
// responses (`GET /devices/{id}/cameras`, the POST/PUT response body)
// and in the agent's cameras.update cmd payload (full list).
//
// CameraID is server-assigned in the form cam1, cam2, ... per device.
// It is stable for the camera's lifetime and embedded in URLs like
// `/preview/<camera_id>` once the new Edge UI live preview lands
// (issue #4). is_lpr is constrained to at most one true per device
// via a DB partial unique index (ADR-030 § 1).
type Camera struct {
	CameraID string `json:"camera_id"`
	Label    string `json:"label"`
	RtspURL  string `json:"rtsp_url"`
	IsLPR    bool   `json:"is_lpr"`
}

// Error codes returned by ValidateCamera. Stable strings — the
// agent's cmd-result envelope carries them back to CP where they
// surface in the audit log and (eventually) on the dashboard.
const (
	CodeBadLabel     = "cameras.bad_label"
	CodeBadRtspURL   = "cameras.bad_rtsp_url"
	CodeBadPayload   = "cameras.bad_payload"
	CodeUnknownField = "cameras.unknown_field"
)

// ValidationError carries a stable Code + human Message. Callers
// wrap it in whatever envelope is appropriate for their boundary —
// the API translates to an HTTP 400 body; the agent's cameras.update
// handler will wrap in envelope.CodedError.
type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// AsValidation extracts a *ValidationError from err if present.
func AsValidation(err error) (*ValidationError, bool) {
	var v *ValidationError
	if errors.As(err, &v) {
		return v, true
	}
	return nil, false
}

// ValidateCamera enforces the per-camera invariants the API and the
// agent must agree on: a non-empty label (whitespace-trimmed) and an
// rtsp:// or rtsps:// scheme on the URL. Length / characterset rules
// for the rest of the URL are intentionally permissive — vendor URLs
// commonly embed credentials with special characters and stricter
// parsing would reject valid input.
func ValidateCamera(label, rtspURL string) error {
	if strings.TrimSpace(label) == "" {
		return &ValidationError{Code: CodeBadLabel, Message: "label is required"}
	}
	if !strings.HasPrefix(rtspURL, "rtsp://") && !strings.HasPrefix(rtspURL, "rtsps://") {
		return &ValidationError{
			Code:    CodeBadRtspURL,
			Message: "rtsp_url must begin with rtsp:// or rtsps://",
		}
	}
	return nil
}
