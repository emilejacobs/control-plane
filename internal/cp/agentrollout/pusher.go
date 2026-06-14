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
	"io"
	"log/slog"
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

// CommandSigner signs the agent.update envelope before it is published
// (issue #41). *cmdsign.Signer satisfies it. nil leaves the command unsigned
// — the ADR-028 forward-compat path.
type CommandSigner interface {
	Sign(cmd envelope.Command) (envelope.Command, error)
}

// DefaultURLTTL bounds the presigned artifact URLs. A pushed device fetches
// immediately; an offline device gets a fresh push (fresh URLs) from the
// reconcile path when it reconnects, so the TTL only needs to cover one
// download attempt.
const DefaultURLTTL = 15 * time.Minute

// Pusher publishes agent.update commands. Zero-value fields fall back to
// DefaultURLTTL / crypto-random command ids / time.Now / a discard logger.
type Pusher struct {
	Manifests ManifestSource
	Presigner Presigner
	Publisher CmdPublisher
	URLTTL    time.Duration
	NewCmdID  func() string
	Now       func() time.Time
	Logger    *slog.Logger
	// Signer, when set, signs the agent.update command envelope so a
	// verifying agent accepts it (issue #41). nil publishes unsigned.
	Signer CommandSigner
}

// Push publishes one agent.update {manifest, urls} command on
// devices/{deviceID}/cmd: the signed manifest verbatim plus a presigned GET
// URL per artifact platform (the agent picks its own). Any failure aborts
// without publishing — a half-presigned urls map would strand some platforms
// on an unfetchable update.
func (p *Pusher) Push(ctx context.Context, deviceID, version, correlationID string) error {
	args, err := p.prepareArgs(ctx, version)
	if err != nil {
		return err
	}
	return p.publish(ctx, deviceID, args, correlationID)
}

// PushMany fans the same agent.update out to a target set, fetching the
// manifest and presigning once. A per-device publish failure does not abort
// the rest — the reconcile path re-pushes that device on its next
// heartbeat/reconnect — it just isn't counted in pushed. The returned error
// is non-nil only for up-front failures (catalog miss, presign).
func (p *Pusher) PushMany(ctx context.Context, deviceIDs []string, version, correlationID string) (int, error) {
	args, err := p.prepareArgs(ctx, version)
	if err != nil {
		return 0, err
	}
	pushed := 0
	for _, id := range deviceIDs {
		if err := p.publish(ctx, id, args, correlationID); err != nil {
			p.logger().Warn("agent.update push failed; reconcile will retry",
				"device_id", id, "version", version, "err", err)
			continue
		}
		pushed++
	}
	return pushed, nil
}

// prepareArgs fetches the signed manifest for version and presigns every
// artifact, returning the marshalled protoupdate.Args.
func (p *Pusher) prepareArgs(ctx context.Context, version string) (json.RawMessage, error) {
	m, err := p.Manifests.Manifest(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("manifest %s: %w", version, err)
	}

	ttl := p.URLTTL
	if ttl <= 0 {
		ttl = DefaultURLTTL
	}
	urls := make(map[string]string, len(m.Artifacts))
	for platform, art := range m.Artifacts {
		u, err := p.Presigner.GetURL(ctx, art.URL, ttl)
		if err != nil {
			return nil, fmt.Errorf("presign %s artifact: %w", platform, err)
		}
		urls[platform] = u
	}

	args, err := json.Marshal(protoupdate.Args{Manifest: m, URLs: urls})
	if err != nil {
		return nil, fmt.Errorf("encode agent.update args: %w", err)
	}
	return args, nil
}

func (p *Pusher) publish(ctx context.Context, deviceID string, args json.RawMessage, correlationID string) error {
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
	if p.Signer != nil {
		signed, err := p.Signer.Sign(cmd)
		if err != nil {
			return fmt.Errorf("sign agent.update command: %w", err)
		}
		cmd = signed
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

func (p *Pusher) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
