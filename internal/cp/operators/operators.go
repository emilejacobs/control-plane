// Package operators is the management surface for CP operators (issue #16):
// list / view / create / edit / deactivate the local-credential accounts
// coworkers log in with. It is distinct from package authn, which owns the
// authentication path (login, TOTP, refresh) over the same operators table —
// this package owns the staff-driven administration of those rows.
package operators

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by Get for an operator id that matches no row
// (including a non-UUID id). Handlers translate it to HTTP 404.
var ErrNotFound = errors.New("operator not found")

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

func scanOperator(rows pgx.Rows) (Operator, error) {
	var op Operator
	if err := rows.Scan(&op.ID, &op.Email, &op.IsStaff, &op.TotpEnrolled, &op.Deactivated, &op.SiteIDs); err != nil {
		return Operator{}, fmt.Errorf("scan operator: %w", err)
	}
	return op, nil
}
