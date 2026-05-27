package middleware

import "net/http"

// RequireStaff returns middleware that 403s any non-staff operator.
// Used for admin-only surfaces (e.g. taxonomy sync — ADR-033 § 8 —
// or the Operators page). Runs after Auth so the operator's claims
// are in context; reads is_staff straight off the JWT.
func RequireStaff() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := OperatorFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			if !claims.IsStaff {
				http.Error(w, "staff only", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
