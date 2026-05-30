package agentmanifest

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func sampleManifest() Manifest {
	return Manifest{
		Version: "1.2.3",
		Artifacts: map[string]Artifact{
			"darwin/arm64": {URL: "https://dist/agent/1.2.3/darwin-arm64", SHA256: "aaaa"},
			"darwin/amd64": {URL: "https://dist/agent/1.2.3/darwin-amd64", SHA256: "bbbb"},
			"linux/arm64":  {URL: "https://dist/agent/1.2.3/linux-arm64", SHA256: "cccc"},
		},
	}
}

// TestSignVerifyRoundTrip — a manifest signed with the private key verifies
// against the matching public key.
func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	signed, err := Sign(priv, sampleManifest())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if signed.Signature == "" {
		t.Fatal("Sign left Signature empty")
	}
	if err := Verify(pub, signed); err != nil {
		t.Errorf("Verify(valid) = %v, want nil", err)
	}
}

// TestVerifyRejectsTamper — any change to the signed content (here an
// artifact's sha256) invalidates the signature.
func TestVerifyRejectsTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signed, _ := Sign(priv, sampleManifest())

	tampered := signed
	tampered.Artifacts = map[string]Artifact{
		"darwin/arm64": {URL: "https://dist/agent/1.2.3/darwin-arm64", SHA256: "EVIL"},
		"darwin/amd64": signed.Artifacts["darwin/amd64"],
		"linux/arm64":  signed.Artifacts["linux/arm64"],
	}
	if err := Verify(pub, tampered); !errors.Is(err, ErrBadSignature) {
		t.Errorf("Verify(tampered) = %v, want ErrBadSignature", err)
	}
}

// TestVerifyRejectsWrongKey — a signature doesn't verify under a different key.
func TestVerifyRejectsWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	otherPub, _, _ := ed25519.GenerateKey(nil)
	signed, _ := Sign(priv, sampleManifest())

	if err := Verify(otherPub, signed); !errors.Is(err, ErrBadSignature) {
		t.Errorf("Verify(wrong key) = %v, want ErrBadSignature", err)
	}
}

// TestArtifactFor — resolves the artifact for a platform, or reports absence.
func TestArtifactFor(t *testing.T) {
	m := sampleManifest()
	a, ok := m.ArtifactFor("darwin", "arm64")
	if !ok || a.SHA256 != "aaaa" {
		t.Errorf("ArtifactFor(darwin/arm64) = %+v, %v", a, ok)
	}
	if _, ok := m.ArtifactFor("windows", "amd64"); ok {
		t.Error("ArtifactFor(windows/amd64) = ok, want absent")
	}
}
