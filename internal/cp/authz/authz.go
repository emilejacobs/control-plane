// Package authz owns site-scoped authorization: resolving an operator's site
// allowlist and composing it into every device-touching query.
//
// Per PRD § AuthZ and architecture.md § Security, every device-returning
// handler must route through ScopedDeviceQuery so a future site-scoped
// operator cannot be silently over-served. Phase 1 operators are all staff
// (is_staff = true), whose SiteFilter is All — the single exercised branch.
package authz

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SiteFilter is the set of sites an operator may see. All is the staff
// full-fleet grant; otherwise SiteIDs is the explicit allowlist (empty means
// the operator sees nothing — fail-closed).
type SiteFilter struct {
	All     bool
	SiteIDs []string
}

// AuthZ resolves operator site allowlists. The pool is used only for the
// non-staff path; staff scopes resolve from the JWT claim alone.
type AuthZ struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *AuthZ { return &AuthZ{pool: pool} }

// ScopeForOperator resolves the operator's SiteFilter. A staff operator gets
// the All grant; a non-staff operator gets the sites listed in operator_sites
// — an empty list if none, which fails closed.
func (z *AuthZ) ScopeForOperator(ctx context.Context, operatorID string, isStaff bool) (SiteFilter, error) {
	if isStaff {
		return SiteFilter{All: true}, nil
	}
	rows, err := z.pool.Query(ctx,
		`SELECT site_id::text FROM operator_sites WHERE operator_id = $1`, operatorID)
	if err != nil {
		return SiteFilter{}, fmt.Errorf("query operator sites: %w", err)
	}
	siteIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return SiteFilter{}, fmt.Errorf("collect operator sites: %w", err)
	}
	return SiteFilter{SiteIDs: siteIDs}, nil
}
