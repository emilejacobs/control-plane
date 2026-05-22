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

type scopeCtxKey struct{}

// ContextWithScope returns ctx carrying the operator's resolved SiteFilter.
// The scope middleware sets it; ScopedDeviceQuery callers read it back.
func ContextWithScope(ctx context.Context, f SiteFilter) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, f)
}

// ScopeFromContext returns the SiteFilter the scope middleware injected. The
// second result is false when no scope was set — callers must fail closed.
func ScopeFromContext(ctx context.Context) (SiteFilter, bool) {
	f, ok := ctx.Value(scopeCtxKey{}).(SiteFilter)
	return f, ok
}

// AuthZ resolves operator site allowlists. The pool is used only for the
// non-staff path; staff scopes resolve from the JWT claim alone.
type AuthZ struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *AuthZ { return &AuthZ{pool: pool} }

// ScopedDeviceQuery composes an operator's SiteFilter into a query against the
// devices table. baseSQL must end at its WHERE clause (use `WHERE true` when
// there is no other condition); the caller appends any ORDER BY / LIMIT to the
// returned SQL. A staff (All) filter imposes no restriction; otherwise a
// `site_id = ANY(...)` predicate is appended and the site-id list is bound as
// the next positional argument.
//
// Every device-returning read must route through this helper — the CI gate
// fails any handler whose devices query bypasses it.
func ScopedDeviceQuery(f SiteFilter, baseSQL string, args ...any) (string, []any) {
	if f.All {
		return baseSQL, args
	}
	predicate := fmt.Sprintf(" AND site_id = ANY($%d)", len(args)+1)
	return baseSQL + predicate, append(args, f.SiteIDs)
}

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
