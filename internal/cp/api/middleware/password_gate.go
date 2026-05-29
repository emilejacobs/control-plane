package middleware

import (
	"context"
	"net/http"
)

// passwordChangeReason is the value of the Reason header on a gate rejection
// — the machine-readable code a client branches on to route into the
// set-new-password flow.
const passwordChangeReason = "password-change-required"

// PasswordChangeChecker reports whether an operator is still on a
// system-generated temp password and must set a new one. *authn.AuthN
// satisfies it.
type PasswordChangeChecker interface {
	MustChangePassword(ctx context.Context, operatorID string) (bool, error)
}

// RequirePasswordChanged returns middleware that blocks an operator who must
// still change a system-generated temp password: every wrapped route answers
// 403 with a "Reason: password-change-required" header until they do. It runs
// after Auth — reading the operator id from the request context — and is
// applied to every protected route except the set-new-password endpoint
// itself (#16).
func RequirePasswordChanged(c PasswordChangeChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := OperatorFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			must, err := c.MustChangePassword(r.Context(), claims.OperatorID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if must {
				w.Header().Set("Reason", passwordChangeReason)
				http.Error(w, "password change required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
