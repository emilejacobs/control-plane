package agentupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
	protoupdate "github.com/emilejacobs/control-plane/internal/protocol/agentupdate"
)

// signedManifestFor builds a manifest whose linux/amd64 artifact's sha256
// matches binBytes, signed with a fresh test key, and returns the manifest
// JSON + the matching public key.
func signedManifestFor(t *testing.T, binBytes []byte) (json.RawMessage, ed25519.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	sum := sha256.Sum256(binBytes)
	m, err := agentmanifest.Sign(priv, agentmanifest.Manifest{
		Version: "1.4.0",
		Artifacts: map[string]agentmanifest.Artifact{
			"linux/amd64": {URL: "https://dist/agent/1.4.0/linux-amd64", SHA256: hex.EncodeToString(sum[:])},
		},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, _ := json.Marshal(m)
	return raw, pub
}

func newHandler(t *testing.T, pub ed25519.PublicKey, fetch func(context.Context, string) ([]byte, error)) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	return &Handler{
		Verify: func(m agentmanifest.Manifest) error { return agentmanifest.Verify(pub, m) },
		Fetch:  fetch,
		Dir:    dir,
		GOOS:   "linux",
		GOARCH: "amd64",
	}, dir
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var c *envelope.CodedError
	if !errors.As(err, &c) {
		t.Fatalf("error %v is not a CodedError", err)
	}
	return c.Code
}

// TestApplyStagesCandidate — a valid signed manifest stages the fetched binary
// as the candidate, writes the version, and flags "trying".
func TestApplyStagesCandidate(t *testing.T) {
	bin := []byte("new-agent-binary-bytes")
	raw, pub := signedManifestFor(t, bin)
	h, dir := newHandler(t, pub, func(context.Context, string) ([]byte, error) { return bin, nil })

	res, err := h.Handle(context.Background(), raw)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got := res.(protoupdate.Result)
	if !got.Staged || got.Version != "1.4.0" {
		t.Fatalf("result = %+v, want staged 1.4.0", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "candidate")); string(b) != string(bin) {
		t.Errorf("candidate bytes = %q, want the fetched binary", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "trying")); err != nil {
		t.Errorf("trying flag missing: %v", err)
	}
	if v, _ := os.ReadFile(filepath.Join(dir, "candidate.version")); string(v) != "1.4.0" {
		t.Errorf("candidate.version = %q, want 1.4.0", v)
	}
}

// TestApplyRejectsSha256Mismatch — fetched bytes whose digest ≠ the manifest's
// are refused; nothing is staged.
func TestApplyRejectsSha256Mismatch(t *testing.T) {
	raw, pub := signedManifestFor(t, []byte("intended-bytes"))
	h, dir := newHandler(t, pub, func(context.Context, string) ([]byte, error) {
		return []byte("DIFFERENT-bytes"), nil
	})

	_, err := h.Handle(context.Background(), raw)
	if codeOf(t, err) != protoupdate.CodeSHA256Mismatch {
		t.Errorf("code = %q, want %q", codeOf(t, err), protoupdate.CodeSHA256Mismatch)
	}
	if _, err := os.Stat(filepath.Join(dir, "candidate")); !os.IsNotExist(err) {
		t.Error("candidate staged despite sha256 mismatch")
	}
}

// TestApplyRejectsBadSignature — a manifest that fails verification is refused.
func TestApplyRejectsBadSignature(t *testing.T) {
	bin := []byte("x")
	raw, _ := signedManifestFor(t, bin)
	otherPub, _, _ := ed25519.GenerateKey(nil) // not the signing key
	h, _ := newHandler(t, otherPub, func(context.Context, string) ([]byte, error) { return bin, nil })

	_, err := h.Handle(context.Background(), raw)
	if codeOf(t, err) != protoupdate.CodeBadSignature {
		t.Errorf("code = %q, want %q", codeOf(t, err), protoupdate.CodeBadSignature)
	}
}

// TestApplyUnsupportedPlatform — no artifact for the running platform.
func TestApplyUnsupportedPlatform(t *testing.T) {
	bin := []byte("x")
	raw, pub := signedManifestFor(t, bin)
	h, _ := newHandler(t, pub, func(context.Context, string) ([]byte, error) { return bin, nil })
	h.GOARCH = "riscv64" // manifest only has linux/amd64

	_, err := h.Handle(context.Background(), raw)
	if codeOf(t, err) != protoupdate.CodeUnsupportedPlatform {
		t.Errorf("code = %q, want %q", codeOf(t, err), protoupdate.CodeUnsupportedPlatform)
	}
}

// TestApplyPrefersPresignedURL — issue #40: CP sends {manifest, urls} where
// urls carries presigned GET URLs per platform (the manifest's own artifact
// URLs are private S3 keys, covered by the signature, so CP can't rewrite
// them). The handler fetches from its platform's presigned URL; integrity is
// still anchored to the signed sha256.
func TestApplyPrefersPresignedURL(t *testing.T) {
	bin := []byte("presigned-agent-bytes")
	rawManifest, pub := signedManifestFor(t, bin)

	var fetched string
	h, _ := newHandler(t, pub, func(_ context.Context, url string) ([]byte, error) {
		fetched = url
		return bin, nil
	})

	var m agentmanifest.Manifest
	_ = json.Unmarshal(rawManifest, &m)
	args, _ := json.Marshal(protoupdate.Args{
		Manifest: m,
		URLs:     map[string]string{"linux/amd64": "https://s3.presigned/agent?sig=abc"},
	})

	res, err := h.Handle(context.Background(), args)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := res.(protoupdate.Result); !got.Staged || got.Version != "1.4.0" {
		t.Fatalf("result = %+v, want staged 1.4.0", got)
	}
	if fetched != "https://s3.presigned/agent?sig=abc" {
		t.Errorf("fetched %q, want the presigned URL", fetched)
	}
}

// TestApplyFallsBackToArtifactURL — a {manifest, urls} payload with no entry
// for the running platform falls back to the artifact's own URL (dev/bench
// pushes where the URL is directly fetchable).
func TestApplyFallsBackToArtifactURL(t *testing.T) {
	bin := []byte("fallback-bytes")
	rawManifest, pub := signedManifestFor(t, bin)

	var fetched string
	h, _ := newHandler(t, pub, func(_ context.Context, url string) ([]byte, error) {
		fetched = url
		return bin, nil
	})

	var m agentmanifest.Manifest
	_ = json.Unmarshal(rawManifest, &m)
	args, _ := json.Marshal(protoupdate.Args{
		Manifest: m,
		URLs:     map[string]string{"darwin/arm64": "https://s3.presigned/other"},
	})

	if _, err := h.Handle(context.Background(), args); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fetched != "https://dist/agent/1.4.0/linux-amd64" {
		t.Errorf("fetched %q, want the artifact URL fallback", fetched)
	}
}

// TestApplyRejectsDowngrade — issue #41: even a validly-signed manifest whose
// version is older than the agent's current version is refused, and nothing is
// staged. Closes the forged-command downgrade risk (ADR-035 §2).
func TestApplyRejectsDowngrade(t *testing.T) {
	bin := []byte("older-binary")
	raw, pub := signedManifestFor(t, bin) // manifest version is 1.4.0
	h, dir := newHandler(t, pub, func(context.Context, string) ([]byte, error) { return bin, nil })
	h.CurrentVersion = "1.5.0"

	_, err := h.Handle(context.Background(), raw)
	if codeOf(t, err) != protoupdate.CodeDowngradeRejected {
		t.Errorf("code = %q, want %q", codeOf(t, err), protoupdate.CodeDowngradeRejected)
	}
	if _, err := os.Stat(filepath.Join(dir, "candidate")); !os.IsNotExist(err) {
		t.Error("candidate staged despite downgrade")
	}
}

// TestApplyAllowsSameAndNewerVersion — an equal or newer target stages
// normally; the no-downgrade rule only blocks strictly-older targets.
func TestApplyAllowsSameAndNewerVersion(t *testing.T) {
	for _, current := range []string{"1.4.0", "1.3.0", "0.1.0"} {
		bin := []byte("candidate-" + current)
		raw, pub := signedManifestFor(t, bin) // version 1.4.0
		h, _ := newHandler(t, pub, func(context.Context, string) ([]byte, error) { return bin, nil })
		h.CurrentVersion = current

		res, err := h.Handle(context.Background(), raw)
		if err != nil {
			t.Fatalf("current=%s: Handle: %v", current, err)
		}
		if got := res.(protoupdate.Result); !got.Staged {
			t.Errorf("current=%s: not staged", current)
		}
	}
}

// An empty CurrentVersion (handler built without it) skips the rule — the
// no-downgrade guard never blocks when the agent's version is unknown.
func TestApplyNoDowngradeCheckWhenCurrentUnset(t *testing.T) {
	bin := []byte("x")
	raw, pub := signedManifestFor(t, bin)
	h, _ := newHandler(t, pub, func(context.Context, string) ([]byte, error) { return bin, nil })
	// h.CurrentVersion left empty

	if _, err := h.Handle(context.Background(), raw); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}
