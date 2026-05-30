// Command agent-manifest-sign signs an agent release manifest (#38). CI uses
// it at release time: it reads an unsigned manifest (version + per-platform
// artifacts) as JSON on stdin, signs it with the Ed25519 private key from
// AGENT_MANIFEST_SIGNING_KEY (base64), and writes the signed manifest JSON to
// stdout for upload to the agent-dist bucket. The private key never touches
// the repo or argv.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/emilejacobs/control-plane/internal/protocol/agentmanifest"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-manifest-sign:", err)
		os.Exit(1)
	}
}

func run() error {
	keyB64 := os.Getenv("AGENT_MANIFEST_SIGNING_KEY")
	if keyB64 == "" {
		return fmt.Errorf("AGENT_MANIFEST_SIGNING_KEY is required (base64 ed25519 private key)")
	}
	rawKey, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return fmt.Errorf("decode signing key: %w", err)
	}
	if len(rawKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("signing key is %d bytes, want %d (ed25519 private key)", len(rawKey), ed25519.PrivateKeySize)
	}

	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read manifest from stdin: %w", err)
	}
	var m agentmanifest.Manifest
	if err := json.Unmarshal(in, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version == "" || len(m.Artifacts) == 0 {
		return fmt.Errorf("manifest needs a version and at least one artifact")
	}

	signed, err := agentmanifest.Sign(ed25519.PrivateKey(rawKey), m)
	if err != nil {
		return fmt.Errorf("sign manifest: %w", err)
	}
	out, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return fmt.Errorf("encode signed manifest: %w", err)
	}
	_, err = os.Stdout.Write(append(out, '\n'))
	return err
}
