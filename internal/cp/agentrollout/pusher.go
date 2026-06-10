// Package agentrollout owns the CP side of the agent fleet-update push
// (issue #40, ADR-035 §1): building and publishing the agent.update command
// that drives a device toward its desired_agent_version. The same Pusher
// serves both the operator's "set desired" endpoint (cp-api) and the
// reconcile path (cp-ingest re-pushing on reconnect/heartbeat mismatch), so
// the two can't drift on the wire shape.
package agentrollout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
	protoupdate "github.com/emilejacobs/control-plane/internal/protocol/agentupdate"
)

// ErrVersionNotFound means the release catalog carries no manifest for the
// requested version. The API layer maps it to a 4xx at set time so a rollout
// can never target a version that doesn't exist.
var ErrVersionNotFound = errors.New("agent release version not found")

// ManifestSource fetches the signed release manifest for a version from the
// agent-dist catalog (agent/{version}/manifest.json). Implementations return
// ErrVersionNotFound for a version the catalog doesn't carry.
type ManifestSource interface {
	Manifest(ctx context.Context, version string) (agentmanifest.Manifest, error)
}

// Presigner mints a short-lived GET URL for an agent-dist S3 key. The
// manifest's artifact URLs are private S3 keys covered by the signature, so
// the push presigns them alongside rather than rewriting them.
type Presigner interface {
	GetURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// CmdPublisher publishes a serialised envelope.Command to an IoT topic.
type CmdPublisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
}

// DefaultURLTTL bounds the presigned artifact URLs. A pushed device fetches
// immediately; an offline device gets a fresh push (fresh URLs) from the
// reconcile path when it reconnects, so the TTL only needs to cover one
// download attempt.
const DefaultURLTTL = 15 * time.Minute

// Pusher publishes agent.update commands. Zero-value fields fall back to
// DefaultURLTTL / crypto-random command ids / time.Now.
type Pusher struct {
	Manifests ManifestSource
	Presigner Presigner
	Publisher CmdPublisher
	URLTTL    time.Duration
	NewCmdID  func() string
	Now       func() time.Time
}

// Push publishes one agent.update {manifest, urls} command on
// devices/{deviceID}/cmd: the signed manifest verbatim plus a presigned GET
// URL per artifact platform (the agent picks its own). Any failure aborts
// without publishing — a half-presigned urls map would strand some platforms
// on an unfetchable update.
func (p *Pusher) Push(ctx context.Context, deviceID, version, correlationID string) error {
	m, err := p.Manifests.Manifest(ctx, version)
	if err != nil {
		return fmt.Errorf("manifest %s: %w", version, err)
	}

	ttl := p.URLTTL
	if ttl <= 0 {
		ttl = DefaultURLTTL
	}
	urls := make(map[string]string, len(m.Artifacts))
	for platform, art := range m.Artifacts {
		u, err := p.Presigner.GetURL(ctx, art.URL, ttl)
		if err != nil {
			return fmt.Errorf("presign %s artifact: %w", platform, err)
		}
		urls[platform] = u
	}

	args, err := json.Marshal(protoupdate.Args{Manifest: m, URLs: urls})
	if err != nil {
		return fmt.Errorf("encode agent.update args: %w", err)
	}

	newID := p.NewCmdID
	if newID == nil {
		newID = randomID
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	cmd := envelope.Command{
		Type:          "agent.update",
		CorrelationID: correlationID,
		CommandID:     newID(),
		Args:          args,
		IssuedAt:      now().UTC(),
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("encode agent.update command: %w", err)
	}
	if err := p.Publisher.Publish(ctx, "devices/"+deviceID+"/cmd", payload); err != nil {
		return fmt.Errorf("publish agent.update to %s: %w", deviceID, err)
	}
	return nil
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
