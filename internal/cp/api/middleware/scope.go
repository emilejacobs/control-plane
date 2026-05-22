package middleware

import (
	"context"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/authz"
)

// ScopeResolver resolves an operator's site allowlist. *authz.AuthZ satisfies
// it; the interface keeps the middleware off the full AuthZ surface.
type ScopeResolver interface {
	ScopeForOperator(ctx context.Context, operatorID string, isStaff bool) (authz.SiteFilter, error)
}

// Scope returns middleware that resolves the operator's SiteFilter and injects
// it into the request context, where ScopedDeviceQuery reads it. It runs after
// Auth — the operator claims must already be in context.
func Scope(r ScopeResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			claims, ok := OperatorFromContext(req.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			filter, err := r.ScopeForOperator(req.Context(), claims.OperatorID, claims.IsStaff)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			next.ServeHTTP(w, req.WithContext(authz.ContextWithScope(req.Context(), filter)))
		})
	}
}
