package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSystemAlreadyInitialized is returned by ClaimFirstRunAdmin once an
// operator row exists. Handlers translate to HTTP 410 Gone.
var ErrSystemAlreadyInitialized = errors.New("system already initialized")

// Config is the per-instance AuthN configuration. SigningKey is required;
// the TTL fields default to ADR-010 values (1h access, 24h refresh) when
// zero.
type Config struct {
	SigningKey      []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

// AuthN is the deep module for operator authentication: password handling,
// JWT issuance, refresh-token lifecycle, first-run-admin bootstrap, lockout.
type AuthN struct {
	pool       *pgxpool.Pool
	signer     *Signer
	refreshTTL time.Duration
}

func New(pool *pgxpool.Pool, cfg Config) *AuthN {
	accessTTL := cfg.AccessTokenTTL
	if accessTTL == 0 {
		accessTTL = time.Hour
	}
	refreshTTL := cfg.RefreshTokenTTL
	if refreshTTL == 0 {
		refreshTTL = 24 * time.Hour
	}
	return &AuthN{
		pool:       pool,
		signer:     NewSigner(cfg.SigningKey, accessTTL),
		refreshTTL: refreshTTL,
	}
}

// Tokens is the access + refresh pair returned to clients on first-run,
// login, and refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
}

// ClaimFirstRunAdmin atomically (modulo the EXISTS race, bounded by the
// UNIQUE constraint on email) creates the first operator row and returns
// a token pair. Subsequent calls return ErrSystemAlreadyInitialized.
func (a *AuthN) ClaimFirstRunAdmin(ctx context.Context, email, password string) (Tokens, error) {
	var existing int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM operators`).Scan(&existing); err != nil {
		return Tokens{}, fmt.Errorf("count operators: %w", err)
	}
	if existing > 0 {
		return Tokens{}, ErrSystemAlreadyInitialized
	}

	hash, err := HashPassword(password)
	if err != nil {
		return Tokens{}, fmt.Errorf("hash password: %w", err)
	}

	email = strings.ToLower(strings.TrimSpace(email))
	var operatorID string
	err = a.pool.QueryRow(ctx, `
		INSERT INTO operators (email, password_hash, is_staff)
		VALUES ($1, $2, true)
		RETURNING id
	`, email, hash).Scan(&operatorID)
	if err != nil {
		return Tokens{}, fmt.Errorf("insert operator: %w", err)
	}

	return a.issueTokens(ctx, operatorID, email, true)
}

func (a *AuthN) issueTokens(ctx context.Context, operatorID, email string, isStaff bool) (Tokens, error) {
	access, err := a.signer.Issue(TokenClaims{
		OperatorID: operatorID,
		Email:      email,
		IsStaff:    isStaff,
	})
	if err != nil {
		return Tokens{}, fmt.Errorf("sign access token: %w", err)
	}

	refresh, hashBytes, err := newRefreshToken()
	if err != nil {
		return Tokens{}, fmt.Errorf("mint refresh token: %w", err)
	}
	if _, err := a.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, operator_id, expires_at)
		VALUES ($1, $2, $3)
	`, hashBytes, operatorID, time.Now().Add(a.refreshTTL)); err != nil {
		return Tokens{}, fmt.Errorf("insert refresh token: %w", err)
	}

	return Tokens{AccessToken: access, RefreshToken: refresh}, nil
}

// newRefreshToken returns (raw token to send to client, sha256 to persist).
func newRefreshToken() (raw string, hash []byte, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}
