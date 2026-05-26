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
