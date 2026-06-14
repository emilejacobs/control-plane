package integration_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/agentrollout"
	"github.com/emilejacobs/control-plane/internal/dispatcher"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
	"github.com/emilejacobs/control-plane/internal/protocol/cmdsign"
)

// capturePublisher records the exact bytes the Pusher puts on the wire.
type capturePublisher struct{ payload []byte }

func (c *capturePublisher) Publish(_ context.Context, _ string, payload []byte) error {
	c.payload = append([]byte(nil), payload...)
	return nil
}

type signManifests struct{ m agentmanifest.Manifest }

func (s signManifests) Manifest(context.Context, string) (agentmanifest.Manifest, error) {
	return s.m, nil
}

// Issue #41 end-to-end contract: a command the CP Pusher signs with the
// private key verifies on the agent dispatcher under the matching public key,
// across the real marshal→wire→unmarshal path — and the agent rejects the
// same command once a single byte is tampered. This guards the canonical
// signing-bytes agreement between the two halves (the part most likely to
// drift).
func TestCommandSigningCPToAgentRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	pubr := &capturePublisher{}
	pusher := &agentrollout.Pusher{
		Manifests: signManifests{m: agentmanifest.Manifest{
			Version:   "v1.5.0",
			Artifacts: map[string]agentmanifest.Artifact{"darwin/arm64": {URL: "agent/v1.5.0/bin", SHA256: "aa"}},
			Signature: "c2lnbmVk",
		}},
		Presigner: convergePresigner{},
		Publisher: pubr,
		Signer:    cmdsign.NewSigner(priv),
	}
	if err := pusher.Push(context.Background(), "dev-1", "v1.5.0", "corr-1"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// The agent dispatcher gates agent.update on the matching public key.
	var handlerRan bool
	d := dispatcher.New(dispatcher.WithSignatureVerification(
		func(cmd envelope.Command) error { return cmdsign.Verify(pub, cmd) },
		"agent.update",
	))
	d.Register("agent.update", dispatcher.HandlerFunc(func(context.Context, json.RawMessage) (any, error) {
		handlerRan = true
		return map[string]string{"ok": "1"}, nil
	}))

	out, err := d.Dispatch(context.Background(), pubr.payload)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	var res envelope.Result
	_ = json.Unmarshal(out, &res)
	if !res.Success || !handlerRan {
		t.Fatalf("signed command rejected by agent: success=%v ran=%v err=%+v", res.Success, handlerRan, res.Error)
	}

	// Tamper one byte of the args; the agent must now reject it.
	var tampered envelope.Command
	_ = json.Unmarshal(pubr.payload, &tampered)
	tampered.Args = json.RawMessage(`{"manifest":{"version":"v9.9.9"}}`)
	tamperedRaw, _ := json.Marshal(tampered)

	handlerRan = false
	out, _ = d.Dispatch(context.Background(), tamperedRaw)
	_ = json.Unmarshal(out, &res)
	if res.Success || handlerRan {
		t.Fatalf("agent accepted a tampered command: success=%v ran=%v", res.Success, handlerRan)
	}
	if res.Error == nil || res.Error.Code != "command.bad_signature" {
		t.Errorf("tampered command error = %+v, want command.bad_signature", res.Error)
	}
}
