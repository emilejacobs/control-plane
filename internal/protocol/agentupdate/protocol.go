// Package agentupdate holds the wire contract for the downward `agent.update`
// command (issue #39, ADR-035 §3): the agent receives a signed release
// manifest, verifies it, fetches its platform's binary, and stages it as the
// update candidate for the resident wrapper to health-gate. Shared so the
// agent handler and the CP publisher can't drift on codes/shape.
package agentupdate

import "github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"

// Args is the wire shape of the agent.update command args as CP publishes it
// (issue #40): the signed release manifest verbatim, plus per-platform
// presigned GET URLs. The manifest's own artifact URLs are private S3 keys
// covered by the Ed25519 signature, so CP cannot rewrite them in place — the
// presigned URLs ride alongside instead, and integrity stays anchored to the
// signed sha256 (a tampered presigned URL yields bytes that fail the digest
// check). URLs is keyed like Manifest.Artifacts ("<GOOS>/<GOARCH>"); the
// agent falls back to the artifact's own URL when its platform has no entry
// (dev/bench pushes where the URL is directly fetchable).
//
// The agent handler also accepts a bare manifest as args (no "manifest" key)
// so a bench operator can publish a manifest by hand from the IoT console.
type Args struct {
	Manifest agentmanifest.Manifest `json:"manifest"`
	URLs     map[string]string      `json:"urls,omitempty"`
}

// Stable result error codes the dashboard/CP can branch on. The signature/
// integrity codes are the security-relevant ones — they mean "refused to
// install".
const (
	CodeBadPayload          = "agent_update.bad_payload"
	CodeBadSignature        = "agent_update.bad_signature"
	CodeUnsupportedPlatform = "agent_update.unsupported_platform"
	CodeDownloadFailed      = "agent_update.download_failed"
	CodeSHA256Mismatch      = "agent_update.sha256_mismatch"
	CodeStageFailed         = "agent_update.stage_failed"
)

// Result is the agent's ACK on the cmd-result topic: the version staged and
// that it is now the pending candidate (the wrapper promotes or rolls it back
// after the restart + health gate).
type Result struct {
	Version string `json:"version"`
	Staged  bool   `json:"staged"`
}
