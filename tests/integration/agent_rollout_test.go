package integration_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/agentrollout"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
	protoupdate "github.com/emilejacobs/control-plane/internal/protocol/agentupdate"
)

// Issue #40 cycle 1: devices.desired_agent_version round-trips through
// SetDesiredAgentVersion + GetByID. NULL (nil) means "untargeted" — a fresh
// enrollment has no desired version until a rollout targets it.
func TestRegistryDesiredAgentVersionRoundTrip(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	devA := enrollForTest(t, srv, "mac-mini-roll-01", "40000000-0000-0000-0000-000000000001")
	devB := enrollForTest(t, srv, "mac-mini-roll-02", "40000000-0000-0000-0000-000000000002")
	scopedCtx := staffCtx(ctx)

	// Fresh devices are untargeted.
	dev, err := srv.Registry.GetByID(scopedCtx, devA)
	if err != nil {
		t.Fatalf("GetByID fresh: %v", err)
	}
	if dev.DesiredAgentVersion != nil {
		t.Errorf("fresh DesiredAgentVersion: got %v want nil", *dev.DesiredAgentVersion)
	}

	// Targeting a set stamps the version on every named device.
	n, err := srv.Registry.SetDesiredAgentVersion(ctx, []string{devA, devB}, "v1.4.0")
	if err != nil {
		t.Fatalf("SetDesiredAgentVersion: %v", err)
	}
	if n != 2 {
		t.Errorf("affected: got %d want 2", n)
	}
	for _, id := range []string{devA, devB} {
		dev, err := srv.Registry.GetByID(scopedCtx, id)
		if err != nil {
			t.Fatalf("GetByID %s: %v", id, err)
		}
		if dev.DesiredAgentVersion == nil || *dev.DesiredAgentVersion != "v1.4.0" {
			t.Errorf("DesiredAgentVersion %s: got %v want v1.4.0", id, dev.DesiredAgentVersion)
		}
	}

	// Re-targeting overwrites (last-wins) — canary promote / abort both
	// reduce to another set.
	if _, err := srv.Registry.SetDesiredAgentVersion(ctx, []string{devA}, "v1.5.0"); err != nil {
		t.Fatalf("SetDesiredAgentVersion (2nd): %v", err)
	}
	dev, err = srv.Registry.GetByID(scopedCtx, devA)
	if err != nil {
		t.Fatalf("GetByID after 2nd set: %v", err)
	}
	if dev.DesiredAgentVersion == nil || *dev.DesiredAgentVersion != "v1.5.0" {
		t.Errorf("DesiredAgentVersion after 2nd set: got %v want v1.5.0", dev.DesiredAgentVersion)
	}
}

// Unknown or non-UUID ids in the target set are skipped, not an error: the
// affected count is the caller's signal (the API layer 404s on count 0).
func TestRegistryDesiredAgentVersionUnknownIDsSkipped(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	devA := enrollForTest(t, srv, "mac-mini-roll-03", "40000000-0000-0000-0000-000000000003")

	n, err := srv.Registry.SetDesiredAgentVersion(
		ctx,
		[]string{devA, "11111111-2222-3333-4444-555555555555", "not-a-uuid"},
		"v1.4.0",
	)
	if err != nil {
		t.Fatalf("SetDesiredAgentVersion: %v", err)
	}
	if n != 1 {
		t.Errorf("affected: got %d want 1", n)
	}
}

// --- issue #40 acceptance: offline-then-reconnect convergence ---

type convergeManifests struct{ m agentmanifest.Manifest }

func (c *convergeManifests) Manifest(context.Context, string) (agentmanifest.Manifest, error) {
	return c.m, nil
}

type convergePresigner struct{}

func (convergePresigner) GetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://presigned.example/" + key, nil
}

type convergePublisher struct {
	mu    sync.Mutex
	calls []struct {
		topic   string
		payload []byte
	}
}

func (p *convergePublisher) Publish(_ context.Context, topic string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, struct {
		topic   string
		payload []byte
	}{topic, append([]byte(nil), payload...)})
	return nil
}

func (p *convergePublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// A device that is offline when a rollout targets it converges when it
// reconnects: the lifecycle `connected` event re-pushes agent.update, and the
// re-pushing stops once a heartbeat reports the desired version. This is the
// ADR-035 §1 convergence engine end-to-end against real Postgres.
func TestAgentRolloutOfflineReconnectConvergence(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	dev := enrollForTest(t, srv, "mac-mini-roll-04", "40000000-0000-0000-0000-000000000004")

	// Rollout targets the device while it is offline (enrollment leaves
	// is_online false): desired is stamped; no push happens here.
	if _, err := srv.Registry.SetDesiredAgentVersion(ctx, []string{dev}, "v1.5.0"); err != nil {
		t.Fatalf("SetDesiredAgentVersion: %v", err)
	}

	pub := &convergePublisher{}
	pusher := &agentrollout.Pusher{
		Manifests: &convergeManifests{m: agentmanifest.Manifest{
			Version: "v1.5.0",
			Artifacts: map[string]agentmanifest.Artifact{
				"darwin/arm64": {URL: "agent/v1.5.0/uknomi-agent-darwin-arm64", SHA256: "aa"},
			},
			Signature: "c2lnbmVk",
		}},
		Presigner: convergePresigner{},
		Publisher: pub,
	}

	pres := presence.New()
	lifecycles := ingest.NewLifecycleIngester(pres, srv.Registry, nil)
	lifecycles.Versions = srv.Registry
	lifecycles.Updates = pusher
	heartbeats := ingest.NewPresenceIngester(pres, srv.Registry, nil)
	heartbeats.Updates = pusher

	// Reconnect: the device comes back still on its enrollment version
	// (0.1.0) → CP re-pushes agent.update with the signed manifest +
	// presigned URLs.
	err := lifecycles.Handle(ctx, ingest.Lifecycle{
		ClientID: dev, EventType: "connected", CorrelationID: "corr-reconnect",
	})
	if err != nil {
		t.Fatalf("lifecycle Handle: %v", err)
	}
	if pub.count() != 1 {
		t.Fatalf("publishes after reconnect: got %d want 1", pub.count())
	}
	if pub.calls[0].topic != "devices/"+dev+"/cmd" {
		t.Errorf("topic = %q", pub.calls[0].topic)
	}
	var cmd envelope.Command
	if err := json.Unmarshal(pub.calls[0].payload, &cmd); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if cmd.Type != "agent.update" {
		t.Errorf("cmd type = %q", cmd.Type)
	}
	var args protoupdate.Args
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		t.Fatalf("args: %v", err)
	}
	if args.Manifest.Version != "v1.5.0" || args.Manifest.Signature == "" {
		t.Errorf("manifest = %+v, want signed v1.5.0", args.Manifest)
	}
	if args.URLs["darwin/arm64"] != "https://presigned.example/agent/v1.5.0/uknomi-agent-darwin-arm64" {
		t.Errorf("urls = %v", args.URLs)
	}

	// A heartbeat still on the old version (update not applied yet, or
	// rolled back) re-pushes again.
	err = heartbeats.Handle(ctx, ingest.Heartbeat{
		DeviceID: dev, CorrelationID: "corr-hb-1", Version: "0.1.0",
	})
	if err != nil {
		t.Fatalf("heartbeat Handle: %v", err)
	}
	if pub.count() != 2 {
		t.Fatalf("publishes after drifted heartbeat: got %d want 2", pub.count())
	}

	// The update lands: a heartbeat reporting the desired version persists
	// it and the re-pushing stops — converged.
	err = heartbeats.Handle(ctx, ingest.Heartbeat{
		DeviceID: dev, CorrelationID: "corr-hb-2", Version: "v1.5.0",
	})
	if err != nil {
		t.Fatalf("heartbeat Handle: %v", err)
	}
	if pub.count() != 2 {
		t.Fatalf("publishes after converged heartbeat: got %d want still 2", pub.count())
	}
	got, err := srv.Registry.GetByID(staffCtx(ctx), dev)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AgentVersion != "v1.5.0" {
		t.Errorf("reported agent_version = %q, want v1.5.0", got.AgentVersion)
	}
	if got.DesiredAgentVersion == nil || *got.DesiredAgentVersion != "v1.5.0" {
		t.Errorf("desired = %v, want v1.5.0", got.DesiredAgentVersion)
	}
}
