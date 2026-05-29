package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

// PasswordSetter is the narrow surface the set-new-password endpoint needs.
// *authn.AuthN satisfies it.
type PasswordSetter interface {
	SetPassword(ctx context.Context, operatorID, newPassword string) error
}

// SetPasswordHandler serves POST /auth/password — the constrained
// set-new-password path an operator on a system-generated temp password
// completes before any normal action (#16). It sits behind Auth only (not the
// must-change or TOTP gates) so a must-change operator can reach it; the
// operator id comes from the bearer token, never the body.
type SetPasswordHandler struct {
	svc   PasswordSetter
	audit audit.Writer
}

func NewSetPassword(svc PasswordSetter, auditW audit.Writer) *SetPasswordHandler {
	return &SetPasswordHandler{svc: svc, audit: auditW}
}

type setPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

func (h *SetPasswordHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.OperatorFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	var req setPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	err := h.svc.SetPassword(r.Context(), claims.OperatorID, req.NewPassword)
	if errors.Is(err, authn.ErrWeakPassword) {
		http.Error(w, "password too short", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.audit != nil {
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action:       "audit.operator_set_password",
			ActorID:      claims.OperatorID,
			ActorType:    audit.ActorOperator,
			ResourceKind: "operator",
			ResourceID:   claims.OperatorID,
			Outcome:      "success",
			SourceIP:     clientIP(r),
			UserAgent:    r.UserAgent(),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}
