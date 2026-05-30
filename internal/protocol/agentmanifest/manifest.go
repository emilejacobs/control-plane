// Package agentmanifest is the signed release manifest for agent self-update
// (issue #38, ADR-035 §2). The manifest is the signed catalog of available
// agent versions: per-platform binary URL + sha256, plus an Ed25519 signature
// over that content. CI signs it at release with an offline private key; the
// agent verifies it with a baked-in public key before installing a binary.
// Shared so the signer (CI) and the verifier (agent) can't drift.
package agentmanifest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrBadSignature is returned by Verify when the signature does not match the
// manifest content under the given public key (tampered manifest, wrong key,
// or unsigned). The agent must treat this as "do not install".
var ErrBadSignature = errors.New("manifest signature invalid")

// Artifact is one platform's binary: where to fetch it and its expected
// sha256 (hex). The agent re-checks the sha256 of the downloaded bytes.
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Manifest is a signed release. Artifacts is keyed by "<GOOS>/<GOARCH>"
// (e.g. "darwin/arm64"). Signature is base64 of the Ed25519 signature over
// the signing payload (everything but Signature itself).
type Manifest struct {
	Version   string              `json:"version"`
	Artifacts map[string]Artifact `json:"artifacts"`
	Signature string              `json:"signature,omitempty"`
}

// signingBytes is the canonical content the signature covers — the manifest
// without its Signature field. encoding/json marshals struct fields in order
// and map keys sorted, so this is deterministic for a given content.
func (m Manifest) signingBytes() ([]byte, error) {
	payload := struct {
		Version   string              `json:"version"`
		Artifacts map[string]Artifact `json:"artifacts"`
	}{Version: m.Version, Artifacts: m.Artifacts}
	return json.Marshal(payload)
}

// Sign returns a copy of m with Signature set to the Ed25519 signature of its
// signing payload.
func Sign(priv ed25519.PrivateKey, m Manifest) (Manifest, error) {
	payload, err := m.signingBytes()
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest signing payload: %w", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))
	return m, nil
}

// Verify checks m's signature against pub. Returns ErrBadSignature for any
// mismatch (tampered content, wrong key, missing/garbled signature).
func Verify(pub ed25519.PublicKey, m Manifest) error {
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return ErrBadSignature
	}
	payload, err := m.signingBytes()
	if err != nil {
		return fmt.Errorf("manifest signing payload: %w", err)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return ErrBadSignature
	}
	return nil
}

// ArtifactFor returns the artifact for a GOOS/GOARCH, or false if the manifest
// carries no binary for that platform.
func (m Manifest) ArtifactFor(goos, goarch string) (Artifact, bool) {
	a, ok := m.Artifacts[goos+"/"+goarch]
	return a, ok
}
