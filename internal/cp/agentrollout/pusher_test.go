package agentrollout

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
	protoupdate "github.com/emilejacobs/control-plane/internal/protocol/agentupdate"
)

type fakeManifests struct {
	manifests map[string]agentmanifest.Manifest
	err       error
}

func (f *fakeManifests) Manifest(_ context.Context, version string) (agentmanifest.Manifest, error) {
	if f.err != nil {
		return agentmanifest.Manifest{}, f.err
	}
	m, ok := f.manifests[version]
	if !ok {
		return agentmanifest.Manifest{}, ErrVersionNotFound
	}
	return m, nil
}

type fakePresigner struct {
	err  error
	ttls []time.Duration
}

func (f *fakePresigner) GetURL(_ context.Context, key string, ttl time.Duration) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.ttls = append(f.ttls, ttl)
	return "https://presigned/" + key, nil
}

type publishCall struct {
	topic   string
	payload []byte
}

type fakePublisher struct {
	err   error
	calls []publishCall
}

func (f *fakePublisher) Publish(_ context.Context, topic string, payload []byte) error {
	f.calls = append(f.calls, publishCall{topic, payload})
	return f.err
}

func testManifest() agentmanifest.Manifest {
	return agentmanifest.Manifest{
		Version: "v1.4.0",
		Artifacts: map[string]agentmanifest.Artifact{
			"darwin/arm64": {URL: "agent/v1.4.0/uknomi-agent-darwin-arm64", SHA256: "aa"},
			"linux/arm64":  {URL: "agent/v1.4.0/uknomi-agent-linux-arm64", SHA256: "bb"},
		},
		Signature: "c2lnbmVk",
	}
}

func newPusher(m *fakeManifests, p *fakePresigner, pub *fakePublisher) *Pusher {
	return &Pusher{Manifests: m, Presigner: p, Publisher: pub}
}

// Push publishes one agent.update command on the device's cmd topic: the
// signed manifest verbatim plus a presigned GET URL per artifact platform.
func TestPushPublishesSignedManifestWithPresignedURLs(t *testing.T) {
	pub := &fakePublisher{}
	pusher := newPusher(
		&fakeManifests{manifests: map[string]agentmanifest.Manifest{"v1.4.0": testManifest()}},
		&fakePresigner{},
		pub,
	)

	err := pusher.Push(context.Background(), "dev-1", "v1.4.0", "corr-9")
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("publish calls: got %d want 1", len(pub.calls))
	}
	if pub.calls[0].topic != "devices/dev-1/cmd" {
		t.Errorf("topic = %q, want devices/dev-1/cmd", pub.calls[0].topic)
	}

	var cmd envelope.Command
	if err := json.Unmarshal(pub.calls[0].payload, &cmd); err != nil {
		t.Fatalf("payload is not an envelope.Command: %v", err)
	}
	if cmd.Type != "agent.update" {
		t.Errorf("cmd.Type = %q, want agent.update", cmd.Type)
	}
	if cmd.CorrelationID != "corr-9" {
		t.Errorf("cmd.CorrelationID = %q, want corr-9", cmd.CorrelationID)
	}
	if cmd.CommandID == "" {
		t.Error("cmd.CommandID is empty")
	}
	if cmd.IssuedAt.IsZero() {
		t.Error("cmd.IssuedAt is zero")
	}

	var args protoupdate.Args
	if err := json.Unmarshal(cmd.Args, &args); err != nil {
		t.Fatalf("cmd.Args is not protoupdate.Args: %v", err)
	}
	// Manifest rides verbatim — signature included.
	if args.Manifest.Signature != "c2lnbmVk" || args.Manifest.Version != "v1.4.0" {
		t.Errorf("manifest not verbatim: %+v", args.Manifest)
	}
	want := map[string]string{
		"darwin/arm64": "https://presigned/agent/v1.4.0/uknomi-agent-darwin-arm64",
		"linux/arm64":  "https://presigned/agent/v1.4.0/uknomi-agent-linux-arm64",
	}
	for platform, url := range want {
		if args.URLs[platform] != url {
			t.Errorf("urls[%s] = %q, want %q", platform, args.URLs[platform], url)
		}
	}
}

// An unknown version surfaces ErrVersionNotFound and nothing is published —
// the API layer turns this into a 4xx at set time, so a rollout can never
// target a version the catalog doesn't carry.
func TestPushUnknownVersionPublishesNothing(t *testing.T) {
	pub := &fakePublisher{}
	pusher := newPusher(&fakeManifests{manifests: map[string]agentmanifest.Manifest{}}, &fakePresigner{}, pub)

	err := pusher.Push(context.Background(), "dev-1", "v9.9.9", "corr-1")
	if !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("err = %v, want ErrVersionNotFound", err)
	}
	if len(pub.calls) != 0 {
		t.Errorf("published despite missing manifest: %d calls", len(pub.calls))
	}
}

// A presign failure aborts the push (a half-presigned urls map would strand
// some platforms on an unfetchable update).
func TestPushPresignFailurePublishesNothing(t *testing.T) {
	pub := &fakePublisher{}
	pusher := newPusher(
		&fakeManifests{manifests: map[string]agentmanifest.Manifest{"v1.4.0": testManifest()}},
		&fakePresigner{err: errors.New("s3 down")},
		pub,
	)

	if err := pusher.Push(context.Background(), "dev-1", "v1.4.0", "corr-1"); err == nil {
		t.Fatal("Push succeeded despite presign failure")
	}
	if len(pub.calls) != 0 {
		t.Errorf("published despite presign failure: %d calls", len(pub.calls))
	}
}
