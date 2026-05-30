// Package agentupdate implements the agent-side handler for the downward
// `agent.update` command (issue #39, ADR-035 §3). It receives a signed
// release manifest, verifies it against the baked-in release key, fetches its
// platform's binary, integrity-checks it, and stages it as the **candidate**
// the resident wrapper health-gates after the restart. The handler never
// promotes or rolls back — that is the wrapper's job, off the health marker;
// a binary too broken to prove itself must not be the one deciding its fate.
//
// Wire codes/shape live in internal/protocol/agentupdate so the agent and CP
// halves can't drift.
package agentupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
	protoupdate "github.com/emilejacobs/control-plane/internal/protocol/agentupdate"
)

// Fetcher downloads the binary at url. Production fetches over HTTP (CP hands
// the agent a presigned URL in #40); tests stub it.
type Fetcher func(ctx context.Context, url string) ([]byte, error)

// Handler stages a verified update candidate. Verify defaults to the baked-in
// release-key check; Dir is the on-disk update root (candidate / trying /
// candidate.version live here); GOOS/GOARCH default to the build's platform.
// OnStaged, if set, is called after a successful stage to request the restart
// that hands control to the wrapper.
type Handler struct {
	Verify   func(agentmanifest.Manifest) error
	Fetch    Fetcher
	Dir      string
	GOOS     string
	GOARCH   string
	OnStaged func()
}

// New builds a Handler with production defaults: VerifyRelease + the build's
// platform. The caller supplies the fetcher, update dir, and restart trigger.
func New(dir string, fetch Fetcher, onStaged func()) *Handler {
	return &Handler{
		Verify:   agentmanifest.VerifyRelease,
		Fetch:    fetch,
		Dir:      dir,
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		OnStaged: onStaged,
	}
}

func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var m agentmanifest.Manifest
	if err := json.Unmarshal(args, &m); err != nil {
		return nil, envelope.NewCodedError(protoupdate.CodeBadPayload, "manifest is not valid JSON")
	}

	if err := h.Verify(m); err != nil {
		if errors.Is(err, agentmanifest.ErrBadSignature) {
			return nil, envelope.NewCodedError(protoupdate.CodeBadSignature, "manifest signature did not verify")
		}
		return nil, envelope.NewCodedError(protoupdate.CodeBadSignature, err.Error())
	}

	art, ok := m.ArtifactFor(h.GOOS, h.GOARCH)
	if !ok {
		return nil, envelope.NewCodedError(protoupdate.CodeUnsupportedPlatform,
			fmt.Sprintf("manifest %s has no %s/%s artifact", m.Version, h.GOOS, h.GOARCH))
	}

	bin, err := h.Fetch(ctx, art.URL)
	if err != nil {
		return nil, envelope.NewCodedError(protoupdate.CodeDownloadFailed, err.Error())
	}
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != art.SHA256 {
		return nil, envelope.NewCodedError(protoupdate.CodeSHA256Mismatch,
			"downloaded binary digest does not match the manifest")
	}

	if err := h.stage(m.Version, bin); err != nil {
		return nil, envelope.NewCodedError(protoupdate.CodeStageFailed, err.Error())
	}
	if h.OnStaged != nil {
		h.OnStaged()
	}
	return protoupdate.Result{Version: m.Version, Staged: true}, nil
}

// stage writes the verified binary as the candidate, records its version, and
// raises the "trying" flag the wrapper checks on (re)launch. Candidate first,
// then flag, so the wrapper never sees a flag without a candidate.
func (h *Handler) stage(version string, bin []byte) error {
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return fmt.Errorf("create update dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.Dir, "candidate"), bin, 0o755); err != nil {
		return fmt.Errorf("write candidate: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.Dir, "candidate.version"), []byte(version), 0o644); err != nil {
		return fmt.Errorf("write candidate version: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.Dir, "trying"), []byte(version), 0o644); err != nil {
		return fmt.Errorf("write trying flag: %w", err)
	}
	return nil
}
