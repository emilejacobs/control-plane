package cmdsign

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

// A Signer built from a base64 private key produces signatures the matching
// public key verifies — the cp-api path (key loaded from Secrets Manager).
func TestSignerFromBase64Key(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	b64 := base64.StdEncoding.EncodeToString(priv)

	key, err := ParsePrivateKey(b64)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	signer := NewSigner(key)

	signed, err := signer.Sign(testCommand())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(pub, signed); err != nil {
		t.Errorf("Verify of signer output: %v", err)
	}
}

func TestParsePrivateKeyRejectsBadInput(t *testing.T) {
	if _, err := ParsePrivateKey("not-base64!!!"); err == nil {
		t.Error("accepted non-base64 key")
	}
	if _, err := ParsePrivateKey(base64.StdEncoding.EncodeToString([]byte("too-short"))); err == nil {
		t.Error("accepted a wrong-length key")
	}
}

type fakeLoader struct {
	key string
	err error
}

func (f fakeLoader) Load(context.Context) (string, error) { return f.key, f.err }

func TestLoadSigner(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	b64 := base64.StdEncoding.EncodeToString(priv)

	signer, err := LoadSigner(context.Background(), fakeLoader{key: b64})
	if err != nil {
		t.Fatalf("LoadSigner: %v", err)
	}
	signed, _ := signer.Sign(testCommand())
	if err := Verify(pub, signed); err != nil {
		t.Errorf("signer from LoadSigner produced an unverifiable signature: %v", err)
	}

	if _, err := LoadSigner(context.Background(), fakeLoader{key: "bad!!!"}); err == nil {
		t.Error("LoadSigner accepted a malformed key")
	}
}
