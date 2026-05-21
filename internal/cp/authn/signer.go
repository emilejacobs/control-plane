package authn

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer used for sanity checks on inbound tokens. Pinned in code; if we
// ever need to rotate it we'll do a coordinated cutover.
const tokenIssuer = "uknomi-cp"

// TokenClaims is the subset of JWT claims that callers care about; the
// JWT itself carries iat/exp/iss alongside these.
type TokenClaims struct {
	OperatorID string
	Email      string
	IsStaff    bool
}

// Signer issues + verifies operator access tokens (HS256 over a shared
// signing key). KMS-backed signing (per ADR-010) is a Phase 2/3 hardening
// cycle; HS256 over an env-loaded key is the Phase 1 implementation.
type Signer struct {
	key []byte
	ttl time.Duration
}

func NewSigner(key []byte, ttl time.Duration) *Signer {
	return &Signer{key: key, ttl: ttl}
}

// Issue returns a freshly-signed JWT carrying claims plus iat/exp/iss.
func (s *Signer) Issue(claims TokenClaims) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":      claims.OperatorID,
		"email":    claims.Email,
		"is_staff": claims.IsStaff,
		"iat":      now.Unix(),
		"exp":      now.Add(s.ttl).Unix(),
		"iss":      tokenIssuer,
	})
	return tok.SignedString(s.key)
}

// Verify parses, validates the signature + standard claims, and returns
// the application-level claim subset. Returns an error for any failure
// mode (wrong signature, wrong alg, expired, wrong issuer, etc.).
func (s *Signer) Verify(tokenString string) (TokenClaims, error) {
	parsed, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.key, nil
	}, jwt.WithIssuer(tokenIssuer), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return TokenClaims{}, err
	}
	if !parsed.Valid {
		return TokenClaims{}, errors.New("token invalid")
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return TokenClaims{}, errors.New("claims are not the expected map shape")
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	isStaff, _ := claims["is_staff"].(bool)
	return TokenClaims{OperatorID: sub, Email: email, IsStaff: isStaff}, nil
}
