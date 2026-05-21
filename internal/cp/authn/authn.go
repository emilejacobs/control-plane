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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSystemAlreadyInitialized is returned by ClaimFirstRunAdmin once an
// operator row exists. Handlers translate to HTTP 410 Gone.
var ErrSystemAlreadyInitialized = errors.New("system already initialized")

// ErrInvalidCredentials is returned by Login for an unknown email or a
// password mismatch. The two cases are deliberately indistinguishable so
// callers can't probe for valid emails. Handlers translate to HTTP 401.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrAccountLocked is returned by Login while an account is inside its
// lockout window. Handlers translate to HTTP 429.
var ErrAccountLocked = errors.New("account locked")

// ErrInvalidRefreshToken is returned by Refresh for a refresh token that is
// unknown, already rotated, or expired. Handlers translate to HTTP 401.
var ErrInvalidRefreshToken = errors.New("invalid refresh token")

// Lockout policy: maxFailedAttempts consecutive failures lock the account
// for lockoutWindow. A successful login clears both the counter and the lock.
const (
	maxFailedAttempts = 5
	lockoutWindow     = 15 * time.Minute
)

// Config is the per-instance AuthN configuration. SigningKey is required;
// the TTL fields default to ADR-010 values (1h access, 24h refresh) when
// zero. Now defaults to time.Now; tests inject a fake clock to drive
// lockout-window expiry.
type Config struct {
	SigningKey      []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	Now             func() time.Time
}

// AuthN is the deep module for operator authentication: password handling,
// JWT issuance, refresh-token lifecycle, first-run-admin bootstrap, lockout.
type AuthN struct {
	pool       *pgxpool.Pool
	signer     *Signer
	refreshTTL time.Duration
	now        func() time.Time
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
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &AuthN{
		pool:       pool,
		signer:     NewSigner(cfg.SigningKey, accessTTL),
		refreshTTL: refreshTTL,
		now:        now,
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

// Login verifies email + password and, on success, issues a fresh token
// pair and clears the failed-login state. An unknown email or a wrong
// password both return ErrInvalidCredentials; an account inside its lockout
// window returns ErrAccountLocked. maxFailedAttempts consecutive failures
// lock the account for lockoutWindow.
func (a *AuthN) Login(ctx context.Context, email, password string) (Tokens, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	var operatorID, hash string
	var isStaff bool
	var lockedUntil *time.Time
	err := a.pool.QueryRow(ctx, `
		SELECT id, password_hash, is_staff, locked_until
		FROM operators WHERE email = $1
	`, email).Scan(&operatorID, &hash, &isStaff, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, ErrInvalidCredentials
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("lookup operator: %w", err)
	}

	// A locked account is refused outright: the password is not checked
	// and the failed-attempt counter is left untouched.
	if lockedUntil != nil && a.now().Before(*lockedUntil) {
		return Tokens{}, ErrAccountLocked
	}

	ok, err := VerifyPassword(password, hash)
	if err != nil {
		return Tokens{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		// Count the failed attempt; the one that crosses the threshold
		// trips the lockout, dated by the AuthN clock so tests can drive
		// window expiry.
		if _, err := a.pool.Exec(ctx, `
			UPDATE operators
			SET failed_login_count = failed_login_count + 1,
			    locked_until = CASE
			        WHEN failed_login_count + 1 >= $2 THEN $3::timestamptz
			        ELSE locked_until
			    END,
			    updated_at = now()
			WHERE id = $1
		`, operatorID, maxFailedAttempts, a.now().Add(lockoutWindow)); err != nil {
			return Tokens{}, fmt.Errorf("record failed login: %w", err)
		}
		return Tokens{}, ErrInvalidCredentials
	}

	if _, err := a.pool.Exec(ctx, `
		UPDATE operators
		SET failed_login_count = 0, locked_until = NULL,
		    last_login_at = now(), updated_at = now()
		WHERE id = $1
	`, operatorID); err != nil {
		return Tokens{}, fmt.Errorf("clear login state: %w", err)
	}

	return a.issueTokens(ctx, operatorID, email, isStaff)
}

// Refresh rotates a refresh token: the presented token is revoked and a
// fresh access + refresh pair is issued. An unknown, already-rotated, or
// expired token returns ErrInvalidRefreshToken. The revoke is a conditional
// UPDATE, so a replayed token affects no rows and only one rotation can win.
func (a *AuthN) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	sum := sha256.Sum256([]byte(refreshToken))

	var operatorID string
	err := a.pool.QueryRow(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
		RETURNING operator_id
	`, sum[:], a.now()).Scan(&operatorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, ErrInvalidRefreshToken
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("rotate refresh token: %w", err)
	}

	var email string
	var isStaff bool
	if err := a.pool.QueryRow(ctx, `
		SELECT email, is_staff FROM operators WHERE id = $1
	`, operatorID).Scan(&email, &isStaff); err != nil {
		return Tokens{}, fmt.Errorf("lookup operator: %w", err)
	}

	return a.issueTokens(ctx, operatorID, email, isStaff)
}

// Authenticate verifies a bearer access token's signature and standard
// claims, returning the operator claims it carries. Auth middleware calls
// this on every protected request.
func (a *AuthN) Authenticate(token string) (TokenClaims, error) {
	return a.signer.Verify(token)
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
	`, hashBytes, operatorID, a.now().Add(a.refreshTTL)); err != nil {
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
