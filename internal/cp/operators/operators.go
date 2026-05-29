// Package operators is the management surface for CP operators (issue #16):
// list / view / create / edit / deactivate the local-credential accounts
// coworkers log in with. It is distinct from package authn, which owns the
// authentication path (login, TOTP, refresh) over the same operators table —
// this package owns the staff-driven administration of those rows.
package operators

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

// ErrNotFound is returned by Get for an operator id that matches no row
// (including a non-UUID id). Handlers translate it to HTTP 404.
var ErrNotFound = errors.New("operator not found")

// ErrEmailTaken is returned by Create when the email is already in use
// (the operators.email UNIQUE constraint). Handlers translate it to HTTP 409.
var ErrEmailTaken = errors.New("email already in use")

// Operator is the read-side projection of one operators row for the
// management UI. SiteIDs is the explicit operator_sites allowlist; it is
// empty for a staff operator (whose access is the full fleet, implicit via
// IsStaff) and for a non-staff operator with no grants yet.
type Operator struct {
	ID           string
	Email        string
	IsStaff      bool
	TotpEnrolled bool
	Deactivated  bool
	SiteIDs      []string
}

// Store is the operators management repository over the shared pool.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const selectOperator = `
	SELECT o.id::text, o.email, o.is_staff,
	       o.totp_secret_encrypted IS NOT NULL AS totp_enrolled,
	       o.deactivated_at IS NOT NULL AS deactivated,
	       COALESCE(
	           array_agg(os.site_id::text) FILTER (WHERE os.site_id IS NOT NULL),
	           '{}'
	       ) AS site_ids
	FROM operators o
	LEFT JOIN operator_sites os ON os.operator_id = o.id
`

// List returns every operator, ordered by email, each with its TOTP-enrolled
// status, active/deactivated state, and site allowlist.
func (s *Store) List(ctx context.Context) ([]Operator, error) {
	rows, err := s.pool.Query(ctx, selectOperator+` GROUP BY o.id ORDER BY o.email`)
	if err != nil {
		return nil, fmt.Errorf("list operators: %w", err)
	}
	defer rows.Close()
	var out []Operator
	for rows.Next() {
		op, err := scanOperator(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// Get returns one operator by id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Operator, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Operator{}, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, selectOperator+` WHERE o.id = $1 GROUP BY o.id`, id)
	if err != nil {
		return Operator{}, fmt.Errorf("get operator: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Operator{}, fmt.Errorf("get operator: %w", err)
		}
		return Operator{}, ErrNotFound
	}
	return scanOperator(rows)
}

// CreateInput is the staff-supplied shape for a new operator. The initial
// password is system-generated, not supplied here. SiteIDs is ignored for a
// staff operator (whose access is the full fleet).
type CreateInput struct {
	Email   string
	IsStaff bool
	SiteIDs []string
}

// CreateResult carries the new operator plus the one-time generated temporary
// password. The plaintext is returned exactly once for the admin to relay
// out-of-band; it is never persisted (only its hash) or logged.
type CreateResult struct {
	Operator     Operator
	TempPassword string
}

// Create inserts a new operator with a generated temp password and
// must_change_password armed, plus its site-allowlist grants, in one
// transaction. A duplicate email returns ErrEmailTaken.
func (s *Store) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))

	temp, err := generateTempPassword()
	if err != nil {
		return CreateResult{}, err
	}
	hash, err := authn.HashPassword(temp)
	if err != nil {
		return CreateResult{}, fmt.Errorf("hash temp password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO operators (email, password_hash, is_staff, must_change_password)
		VALUES ($1, $2, $3, true)
		RETURNING id::text
	`, email, hash, in.IsStaff).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return CreateResult{}, ErrEmailTaken
		}
		return CreateResult{}, fmt.Errorf("insert operator: %w", err)
	}

	if !in.IsStaff {
		for _, siteID := range in.SiteIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO operator_sites (operator_id, site_id) VALUES ($1, $2)`, id, siteID,
			); err != nil {
				return CreateResult{}, fmt.Errorf("grant site %s: %w", siteID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, fmt.Errorf("commit: %w", err)
	}

	op, err := s.Get(ctx, id)
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Operator: op, TempPassword: temp}, nil
}

// UpdateInput is the staff-editable shape of an operator. Each field is
// optional: a nil pointer leaves that attribute unchanged. SiteIDs is a full
// replacement of the allowlist when non-nil (an empty slice clears it).
// ResetTotp, when true, clears the TOTP secret + recovery codes so the
// operator must re-enroll on next login. ResetPassword, when true, generates
// a fresh temp password and re-arms must_change_password.
type UpdateInput struct {
	IsStaff       *bool
	SiteIDs       *[]string
	ResetTotp     bool
	ResetPassword bool
}

// UpdateResult is the refreshed operator plus, only when ResetPassword was
// requested, the one-time generated temp password (never persisted/logged).
type UpdateResult struct {
	Operator     Operator
	TempPassword string
}

// Update applies an edit to an operator atomically — role, site allowlist,
// TOTP reset, and password reset all commit in one transaction, so a failure
// can't leave a half-applied edit (e.g. TOTP cleared but password not reset).
// An id matching no row returns ErrNotFound.
func (s *Store) Update(ctx context.Context, id string, in UpdateInput) (UpdateResult, error) {
	if _, err := uuid.Parse(id); err != nil {
		return UpdateResult{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Touch the row first so a no-op edit still distinguishes existence.
	tag, err := tx.Exec(ctx, `
		UPDATE operators
		SET is_staff = COALESCE($2, is_staff),
		    totp_secret_encrypted = CASE WHEN $3 THEN NULL ELSE totp_secret_encrypted END,
		    recovery_codes_hashed = CASE WHEN $3 THEN NULL ELSE recovery_codes_hashed END,
		    updated_at = now()
		WHERE id = $1
	`, id, in.IsStaff, in.ResetTotp)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("update operator: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return UpdateResult{}, ErrNotFound
	}

	if in.SiteIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM operator_sites WHERE operator_id = $1`, id); err != nil {
			return UpdateResult{}, fmt.Errorf("clear allowlist: %w", err)
		}
		for _, siteID := range *in.SiteIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO operator_sites (operator_id, site_id) VALUES ($1, $2)`, id, siteID,
			); err != nil {
				return UpdateResult{}, fmt.Errorf("grant site %s: %w", siteID, err)
			}
		}
	}

	var tempPassword string
	if in.ResetPassword {
		tempPassword, err = generateTempPassword()
		if err != nil {
			return UpdateResult{}, err
		}
		hash, err := authn.HashPassword(tempPassword)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("hash temp password: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE operators SET password_hash = $2, must_change_password = true, updated_at = now()
			WHERE id = $1
		`, id, hash); err != nil {
			return UpdateResult{}, fmt.Errorf("reset password: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return UpdateResult{}, fmt.Errorf("commit: %w", err)
	}
	op, err := s.Get(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Operator: op, TempPassword: tempPassword}, nil
}

// ResetPassword generates a fresh temp password, re-arms must_change_password,
// and returns the plaintext once (never persisted/logged). ErrNotFound for an
// unknown id.
func (s *Store) ResetPassword(ctx context.Context, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", ErrNotFound
	}
	temp, err := generateTempPassword()
	if err != nil {
		return "", err
	}
	hash, err := authn.HashPassword(temp)
	if err != nil {
		return "", fmt.Errorf("hash temp password: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE operators
		SET password_hash = $2, must_change_password = true, updated_at = now()
		WHERE id = $1
	`, id, hash)
	if err != nil {
		return "", fmt.Errorf("reset password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return temp, nil
}

// SetActive deactivates (soft delete) or reactivates an operator. Deactivation
// stamps deactivated_at; reactivation clears it. ErrNotFound for an unknown id.
func (s *Store) SetActive(ctx context.Context, id string, active bool) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	var tag pgconn.CommandTag
	var err error
	if active {
		tag, err = s.pool.Exec(ctx,
			`UPDATE operators SET deactivated_at = NULL, updated_at = now() WHERE id = $1`, id)
	} else {
		tag, err = s.pool.Exec(ctx,
			`UPDATE operators SET deactivated_at = now(), updated_at = now() WHERE id = $1`, id)
	}
	if err != nil {
		return fmt.Errorf("set active: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// generateTempPassword returns a CSPRNG temporary password — 18 random bytes
// rendered as URL-safe base64 (~24 chars). High-entropy by construction;
// it is single-use because the operator must change it on first login.
func generateTempPassword() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate temp password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func scanOperator(rows pgx.Rows) (Operator, error) {
	var op Operator
	if err := rows.Scan(&op.ID, &op.Email, &op.IsStaff, &op.TotpEnrolled, &op.Deactivated, &op.SiteIDs); err != nil {
		return Operator{}, fmt.Errorf("scan operator: %w", err)
	}
	return op, nil
}
