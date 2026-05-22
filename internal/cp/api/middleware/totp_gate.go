package middleware

import (
	"context"
	"net/http"
)

// totpEnrollmentReason is the value of the Reason header on a gate rejection
// — the machine-readable code a client branches on to route into enrollment.
const totpEnrollmentReason = "totp-enrollment-required"

// TotpEnrollmentChecker reports whether an operator has completed TOTP
// enrollment. *authn.AuthN satisfies it.
type TotpEnrollmentChecker interface {
	TotpEnrolled(ctx context.Context, operatorID string) (bool, error)
}

// RequireTotpEnrolled returns middleware that blocks an operator who has not
// yet enrolled TOTP: every wrapped route answers 403 with a
// "Reason: totp-enrollment-required" header until enrollment completes. It
// runs after Auth — reading the operator id from the request context — and
// is applied to every protected route except POST /auth/totp/enroll itself.
func RequireTotpEnrolled(c TotpEnrollmentChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := OperatorFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			enrolled, err := c.TotpEnrolled(r.Context(), claims.OperatorID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !enrolled {
				w.Header().Set("Reason", totpEnrollmentReason)
				http.Error(w, "totp enrollment required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
