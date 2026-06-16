// Package commission holds the wire type for the commission command — CP →
// agent (#91, ADR-036). It carries the per-device secrets the agent needs to
// bring an assigned device into service: the minted single-use Tailscale auth
// key (to join the tailnet) and, for ALPR devices, the Plate Recognizer license
// + token (to start the container). Cameras are pushed separately via the
// existing cameras.update command.
//
// Shared by cp-api (builds Args at Commission) and the agent handler (consumes
// Args), so the two can't drift. Unsigned in Phase 2 (ADR-028); the Phase 3
// envelope wraps it unchanged.
package commission

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ALPR is the Plate Recognizer config for an ALPR device. Both fields are
// secrets — the agent persists them 0600 and never logs them.
type ALPR struct {
	License string `json:"license"`
	Token   string `json:"token"`
}

// Args is the commission command payload.
type Args struct {
	// TailscaleAuthKey is the minted ephemeral single-use key the agent uses
	// to join the tailnet. Required.
	TailscaleAuthKey string `json:"tailscale_auth_key"`
	// ALPR is present only for devices configured for Plate Recognizer (a
	// per-device license has been set). Nil leaves ALPR untouched.
	ALPR *ALPR `json:"alpr,omitempty"`
}

// ParseArgs decodes + validates a commission payload, rejecting unknown fields,
// a missing auth key, and a half-specified ALPR block.
func ParseArgs(raw json.RawMessage) (Args, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a Args
	if err := dec.Decode(&a); err != nil {
		return Args{}, fmt.Errorf("decode commission args: %w", err)
	}
	if a.TailscaleAuthKey == "" {
		return Args{}, fmt.Errorf("commission: tailscale_auth_key is required")
	}
	if a.ALPR != nil && (a.ALPR.License == "" || a.ALPR.Token == "") {
		return Args{}, fmt.Errorf("commission: alpr requires both license and token")
	}
	return a, nil
}
