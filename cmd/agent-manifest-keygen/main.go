// Command agent-manifest-keygen generates an Ed25519 keypair for signing
// agent release manifests (#38). It prints the PUBLIC key (base64) to stdout
// — commit that as internal/protocol/agentmanifest/release_pubkey.b64 — and
// writes the PRIVATE key (base64) to the file given by -out (default
// ./agent-manifest-signing.key, mode 0600), which you move into the CI
// secret AGENT_MANIFEST_SIGNING_KEY and then delete. The private key is never
// printed to stdout.
//
// Run once per key rotation. The committed dev key MUST be rotated before
// production (regenerate, commit the new public key, set the new CI secret).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("out", "agent-manifest-signing.key", "path to write the base64 private key (0600)")
	flag.Parse()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keygen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write private key:", err)
		os.Exit(1)
	}
	// Public key → stdout (safe to commit); private key → file only.
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	fmt.Fprintf(os.Stderr, "private key written to %s (mode 0600) — move to CI secret AGENT_MANIFEST_SIGNING_KEY, then delete\n", *out)
}
