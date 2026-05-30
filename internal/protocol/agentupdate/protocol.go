// Package agentupdate holds the wire contract for the downward `agent.update`
// command (issue #39, ADR-035 §3): the agent receives a signed release
// manifest, verifies it, fetches its platform's binary, and stages it as the
// update candidate for the resident wrapper to health-gate. Shared so the
// agent handler and the CP publisher can't drift on codes/shape.
package agentupdate

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
