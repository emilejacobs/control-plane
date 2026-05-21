// Package authn owns the Control Plane authentication primitives per
// ADR-010 + PRD § AuthN: Argon2id password hashing, JWT issuance, refresh
// tokens, lockout state, first-run-admin lifecycle.
package authn

import (
	"github.com/alexedwards/argon2id"
)

// argon2idParams pins the OWASP 2024 baseline for Argon2id. The parameters
// are embedded in the resulting PHC hash string so future tunings can roll
// forward without breaking existing hashes.
var argon2idParams = &argon2id.Params{
	Memory:      64 * 1024, // 64 MiB
	Iterations:  3,
	Parallelism: 4,
	SaltLength:  16,
	KeyLength:   32,
}

// HashPassword returns a PHC-formatted Argon2id hash of password. Each call
// produces a fresh random salt — two hashes of the same password differ.
func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, argon2idParams)
}

// VerifyPassword reports whether password matches the PHC-formatted hash.
// The parameters embedded in the hash string are used for verification, so
// rotating argon2idParams above doesn't invalidate older hashes.
func VerifyPassword(password, encodedHash string) (bool, error) {
	return argon2id.ComparePasswordAndHash(password, encodedHash)
}
