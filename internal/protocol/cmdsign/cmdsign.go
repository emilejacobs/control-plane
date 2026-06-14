// Package cmdsign signs and verifies the downward command envelope (issue #41,
// ADR-035 §2): CP signs an envelope.Command with an Ed25519 private key held in
// Secrets Manager (signed in-process — NOT KMS, which has no Ed25519); the
// agent verifies it against a baked-in public key before acting. This closes
// the ADR-028 carve-out for the high-blast-radius `agent.update` command — a
// forged command can't even trigger a version move.
//
// The mechanism mirrors internal/protocol/agentmanifest (same crypto, same
// canonical-bytes discipline), but the keys are DISTINCT and have opposite
// availability: the manifest key is offline (private half a CI secret, never in
// AWS), while the command key is online (private half in Secrets Manager so
// cp-api can sign live). Both public halves are baked into the agent build.
package cmdsign

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/emilejacobs/control-plane/internal/envelope"
)

// ErrBadSignature is returned by Verify for any mismatch: tampered field, wrong
// key, missing signature, or garbled base64. The agent must treat it as "do
// not execute".
var ErrBadSignature = errors.New("command signature invalid")

// signingBytes is the canonical content the signature covers: the command
// without its Signature field. encoding/json emits struct fields in order and
// compacts the json.RawMessage Args verbatim, so a command that survives an
// unmarshal→marshal wire round trip produces identical bytes — letting the
// agent verify the command it received, not the struct CP signed.
func signingBytes(cmd envelope.Command) ([]byte, error) {
	payload := struct {
		CorrelationID string          `json:"correlation_id"`
		CommandID     string          `json:"command_id"`
		Type          string          `json:"type"`
		Args          json.RawMessage `json:"args,omitempty"`
		IssuedAt      string          `json:"issued_at"`
		ExpiresAt     *string         `json:"expires_at,omitempty"`
	}{
		CorrelationID: cmd.CorrelationID,
		CommandID:     cmd.CommandID,
		Type:          cmd.Type,
		Args:          cmd.Args,
		IssuedAt:      cmd.IssuedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
	if cmd.ExpiresAt != nil {
		s := cmd.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		payload.ExpiresAt = &s
	}
	return json.Marshal(payload)
}

// Sign returns a copy of cmd with Signature set to the base64 Ed25519
// signature over its signing payload.
func Sign(priv ed25519.PrivateKey, cmd envelope.Command) (envelope.Command, error) {
	payload, err := signingBytes(cmd)
	if err != nil {
		return envelope.Command{}, fmt.Errorf("command signing payload: %w", err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))
	cmd.Signature = &sig
	return cmd, nil
}

// Verify checks cmd's signature against pub, returning ErrBadSignature for any
// mismatch (tampered content, wrong key, missing or garbled signature).
func Verify(pub ed25519.PublicKey, cmd envelope.Command) error {
	if cmd.Signature == nil {
		return ErrBadSignature
	}
	sig, err := base64.StdEncoding.DecodeString(*cmd.Signature)
	if err != nil {
		return ErrBadSignature
	}
	payload, err := signingBytes(cmd)
	if err != nil {
		return fmt.Errorf("command signing payload: %w", err)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return ErrBadSignature
	}
	return nil
}
