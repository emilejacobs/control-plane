// Package networkscan holds the wire types and validation helpers for
// the Phase 2 Edge UI rework `network.scan` command (issue #3). Both
// the agent's dispatcher handler and the CP-side POST endpoint depend
// on this package so the two halves can't drift on what a valid
// request — or a valid result row — looks like.
//
// `network.scan` is the SIXTH unsigned dispatcher handler (per ADR-028
// extended; ADR-030 § 2 enumerates it). The scanner is read-only on
// the LAN and the agent already runs as root for the IoT cert; the
// blast radius is "more network probe noise" — no privilege
// escalation surface.
package networkscan

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
)

// Error codes returned on the failure path. Stable strings — the
// agent's cmd-result envelope carries them back to CP and on to the
// dashboard's error rendering.
const (
	CodeBadCIDR      = "network_scan.bad_cidr"
	CodeBadPayload   = "network_scan.bad_payload"
	CodeUnknownField = "network_scan.unknown_field"
	CodeScanFailed   = "network_scan.scan_failed"
)

// ValidationError carries a stable Code + human Message. Callers wrap
// it in whatever envelope is appropriate for their boundary — agent
// uses envelope.CodedError; API translates to HTTP 400 body.
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

// Request is the on-wire shape both the API body and the cmd's Args
// field carry. CIDR is optional — when empty the agent auto-detects the
// device's primary subnet and scans that.
type Request struct {
	CIDR string `json:"cidr,omitempty"`
}

// Host is one candidate row in the scan result. IP is the v4 address
// string; MAC is the link-layer address in colon-separated lowercase
// hex (the canonical form most OUI tables use); Vendor is the lookup
// hit or empty string when unknown; OpenPorts is a sorted ascending
// list filtered to the camera-relevant set (80, 443, 554, 8000, 8080).
type Host struct {
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Vendor    string `json:"vendor"`
	OpenPorts []int  `json:"open_ports"`
}

// Response is the success-path agent → cp shape sent in
// envelope.Result.Result. Empty Hosts is a successful scan that found
// nothing, distinct from a scanner failure (which surfaces as a
// CodeScanFailed envelope error instead).
type Response struct {
	Hosts []Host `json:"hosts"`
}

// ParseRequest enforces the field whitelist (rejects unknown fields)
// and the CIDR validation (when non-empty). Empty / absent input is a
// valid request: the agent auto-detects.
func ParseRequest(raw json.RawMessage) (Request, error) {
	if len(raw) == 0 {
		return Request{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return Request{}, &ValidationError{Code: CodeUnknownField, Message: err.Error()}
	}
	if req.CIDR != "" {
		if err := ValidateCIDR(req.CIDR); err != nil {
			return Request{}, err
		}
	}
	return req, nil
}

// ValidateCIDR enforces that the input is a parseable IPv4 CIDR. IPv6
// is rejected — every store LAN we manage is v4, and parsing the user
// input through the v6 path widens the attack surface for no win.
func ValidateCIDR(cidr string) error {
	if strings.TrimSpace(cidr) == "" {
		return &ValidationError{Code: CodeBadCIDR, Message: "cidr is required"}
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return &ValidationError{Code: CodeBadCIDR, Message: err.Error()}
	}
	if !prefix.Addr().Is4() {
		return &ValidationError{Code: CodeBadCIDR, Message: "cidr must be IPv4"}
	}
	return nil
}
