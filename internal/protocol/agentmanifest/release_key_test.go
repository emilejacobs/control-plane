package agentmanifest

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

// TestReleasePublicKeyWellFormed — the embedded release_pubkey.b64 decodes to
// a valid Ed25519 public key (guards against a malformed/empty committed key).
func TestReleasePublicKeyWellFormed(t *testing.T) {
	if got := len(ReleasePublicKey()); got != ed25519.PublicKeySize {
		t.Fatalf("ReleasePublicKey length = %d, want %d", got, ed25519.PublicKeySize)
	}
}

// TestVerifyReleaseRejectsWrongKey — a manifest signed by a key other than
// the baked-in release key fails VerifyRelease.
func TestVerifyReleaseRejectsWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	signed, _ := Sign(priv, sampleManifest())
	if err := VerifyRelease(signed); !errors.Is(err, ErrBadSignature) {
		t.Errorf("VerifyRelease(wrong key) = %v, want ErrBadSignature", err)
	}
}
