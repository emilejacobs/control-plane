package cmdsign

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
)

func testCommand() envelope.Command {
	return envelope.Command{
		CorrelationID: "corr-1",
		CommandID:     "cmd-1",
		Type:          "agent.update",
		Args:          json.RawMessage(`{"manifest":{"version":"v1.5.0"}}`),
		IssuedAt:      time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}
}

// Sign sets the envelope Signature; Verify accepts it under the matching key.
func TestSignThenVerifyRoundTrips(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	signed, err := Sign(priv, testCommand())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if signed.Signature == nil || *signed.Signature == "" {
		t.Fatal("Sign left Signature empty")
	}
	if err := Verify(pub, signed); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// A signature survives a JSON round trip — the agent verifies the command it
// received off the wire, not the in-memory struct CP signed.
func TestVerifyAfterWireRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signed, _ := Sign(priv, testCommand())

	raw, _ := json.Marshal(signed)
	var got envelope.Command
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := Verify(pub, got); err != nil {
		t.Errorf("Verify after wire round trip: %v", err)
	}
}

// Tampering any signed field invalidates the signature; the Signature field
// itself is excluded from the signed payload (so setting it doesn't self-
// invalidate).
func TestVerifyRejectsTampering(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signed, _ := Sign(priv, testCommand())

	cases := map[string]func(*envelope.Command){
		"args":           func(c *envelope.Command) { c.Args = json.RawMessage(`{"manifest":{"version":"v0.0.1"}}`) },
		"type":           func(c *envelope.Command) { c.Type = "service.restart" },
		"command_id":     func(c *envelope.Command) { c.CommandID = "cmd-evil" },
		"correlation_id": func(c *envelope.Command) { c.CorrelationID = "corr-evil" },
		"issued_at":      func(c *envelope.Command) { c.IssuedAt = c.IssuedAt.Add(time.Hour) },
	}
	for name, tamper := range cases {
		c := signed
		tamper(&c)
		if err := Verify(pub, c); err == nil {
			t.Errorf("%s: Verify accepted a tampered command", name)
		}
	}
}

// Wrong key, missing signature, and garbled base64 all fail closed.
func TestVerifyRejectsBadInputs(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	otherPub, _, _ := ed25519.GenerateKey(nil)
	signed, _ := Sign(priv, testCommand())

	if err := Verify(otherPub, signed); err == nil {
		t.Error("Verify accepted a signature from the wrong key")
	}

	unsigned := testCommand()
	if err := Verify(otherPub, unsigned); err == nil {
		t.Error("Verify accepted a command with no signature")
	}

	garbled := signed
	bad := "not-base64!!!"
	garbled.Signature = &bad
	if err := Verify(otherPub, garbled); err == nil {
		t.Error("Verify accepted a garbled signature")
	}
}

// The baked-in command public key decodes to a valid Ed25519 key — the agent
// verifies against it (the matching private half lives in Secrets Manager).
func TestCommandPublicKeyIsValid(t *testing.T) {
	pub := CommandPublicKey()
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("CommandPublicKey size = %d, want %d", len(pub), ed25519.PublicKeySize)
	}
}
