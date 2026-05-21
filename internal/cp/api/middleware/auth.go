package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

// Authenticator verifies a bearer access token and returns the operator's
// claims. *authn.AuthN satisfies this; the interface keeps the middleware
// from depending on the full AuthN surface.
type Authenticator interface {
	Authenticate(token string) (authn.TokenClaims, error)
}

type operatorCtxKey struct{}

// Auth returns middleware that requires a valid Bearer access token. The
// verified operator claims are placed in the request context for handlers
// to read via OperatorFromContext. A missing, malformed, or invalid token
// yields 401 and the wrapped handler never runs.
func Auth(a Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			claims, err := a.Authenticate(token)
			if err != nil {
				http.Error(w, "invalid bearer token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), operatorCtxKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OperatorFromContext returns the authenticated operator's claims when the
// request passed through Auth middleware.
func OperatorFromContext(ctx context.Context) (authn.TokenClaims, bool) {
	claims, ok := ctx.Value(operatorCtxKey{}).(authn.TokenClaims)
	return claims, ok
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. The scheme match is case-insensitive per RFC 7235.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}
