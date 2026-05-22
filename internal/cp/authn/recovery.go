package authn

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

// recoveryCodeCount is the number of single-use recovery codes minted at
// TOTP enrollment (PRD User Story 16).
const recoveryCodeCount = 10

// recoveryCodeEncoding renders raw entropy as lowercase base32 — the codes an
// operator types when their authenticator device is lost.
var recoveryCodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newRecoveryCodes mints recoveryCodeCount fresh recovery codes. It returns
// the plaintext codes — shown to the operator exactly once — alongside their
// Argon2id hashes, which are what gets persisted; the plaintext is never
// stored.
func newRecoveryCodes() (plaintext, hashed []string, err error) {
	for i := 0; i < recoveryCodeCount; i++ {
		var b [10]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, nil, fmt.Errorf("recovery code entropy: %w", err)
		}
		code := strings.ToLower(recoveryCodeEncoding.EncodeToString(b[:]))
		hash, err := HashPassword(code)
		if err != nil {
			return nil, nil, fmt.Errorf("hash recovery code: %w", err)
		}
		plaintext = append(plaintext, code)
		hashed = append(hashed, hash)
	}
	return plaintext, hashed, nil
}
