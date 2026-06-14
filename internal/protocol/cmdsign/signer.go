package cmdsign

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"

	"github.com/emilejacobs/control-plane/internal/envelope"
)

// KeyLoader fetches the base64 command-signing private key from its store of
// record — AWS Secrets Manager in production (bootstrap.SecretsManagerLoader
// satisfies it), a fake in tests.
type KeyLoader interface {
	Load(ctx context.Context) (string, error)
}

// LoadSigner builds a Signer from the key the loader yields, failing fast on a
// missing or malformed key so a misconfigured service does not start up only
// to sign garbage the fleet rejects.
func LoadSigner(ctx context.Context, loader KeyLoader) (*Signer, error) {
	b64, err := loader.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load command signing key: %w", err)
	}
	key, err := ParsePrivateKey(b64)
	if err != nil {
		return nil, err
	}
	return NewSigner(key), nil
}

// Signer holds an Ed25519 private key and signs command envelopes in-process.
// cp-api / cp-ingest build one from the key they load out of Secrets Manager
// (ADR-035 §2: in-process Ed25519, not KMS) and inject it into the rollout
// Pusher.
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSigner wraps a private key.
func NewSigner(priv ed25519.PrivateKey) *Signer {
	return &Signer{priv: priv}
}

// Sign sets the command's Signature using the held key.
func (s *Signer) Sign(cmd envelope.Command) (envelope.Command, error) {
	return Sign(s.priv, cmd)
}

// ParsePrivateKey decodes a base64 Ed25519 private key (the form stored in
// Secrets Manager), validating its length so a misconfigured secret fails at
// startup rather than producing garbage signatures.
func ParsePrivateKey(b64 string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode command signing key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("command signing key is %d bytes, want %d (ed25519 private key)", len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}
