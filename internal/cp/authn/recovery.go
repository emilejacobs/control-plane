package authn

import (
	"context"
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

// consumeRecoveryCode checks code against the operator's stored recovery-code
// hashes. On a match it removes that hash — recovery codes are single-use —
// and returns true; with no match it returns false. The compare is Argon2id,
// tolerating each code's random salt. The UPDATE is conditional on the
// unchanged array, so a concurrent recovery-code login can consume only once.
func (a *AuthN) consumeRecoveryCode(ctx context.Context, operatorID, code string) (bool, error) {
	var hashes []string
	if err := a.pool.QueryRow(ctx,
		`SELECT recovery_codes_hashed FROM operators WHERE id = $1`, operatorID,
	).Scan(&hashes); err != nil {
		return false, fmt.Errorf("lookup recovery codes: %w", err)
	}

	for i, h := range hashes {
		ok, err := VerifyPassword(code, h)
		if err != nil {
			return false, fmt.Errorf("verify recovery code: %w", err)
		}
		if !ok {
			continue
		}
		remaining := append(append([]string{}, hashes[:i]...), hashes[i+1:]...)
		tag, err := a.pool.Exec(ctx, `
			UPDATE operators SET recovery_codes_hashed = $2, updated_at = now()
			WHERE id = $1 AND recovery_codes_hashed = $3::text[]
		`, operatorID, remaining, hashes)
		if err != nil {
			return false, fmt.Errorf("consume recovery code: %w", err)
		}
		// 0 rows means a concurrent login already consumed a code.
		return tag.RowsAffected() == 1, nil
	}
	return false, nil
}
