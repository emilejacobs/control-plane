// Package auth serves /auth/* endpoints: first-run admin bootstrap,
// login, refresh, and TOTP enrollment.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

// clientIP returns the request's source address without the port, falling
// back to the raw RemoteAddr when SplitHostPort is unhappy. Duplicates a
// helper that already exists in middleware/ratelimit.go and enrollment/
// — a shared internal/cp/api/httpx package would consolidate them but is
// not yet worth its own slice.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type Service interface {
	ClaimFirstRunAdmin(ctx context.Context, email, password string) (authn.Tokens, error)
	Login(ctx context.Context, in authn.LoginInput) (authn.LoginResult, error)
	Refresh(ctx context.Context, refreshToken string) (authn.Tokens, error)
	Logout(ctx context.Context, refreshToken string) error
}

type tokensResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// loginResponse is the POST /auth/login body. It adds requires_totp_enrollment
// to the token pair — true when the operator must still enroll TOTP, which
// the client uses to route into the enrollment flow.
type loginResponse struct {
	AccessToken            string `json:"access_token"`
	RefreshToken           string `json:"refresh_token"`
	RequiresTotpEnrollment bool   `json:"requires_totp_enrollment"`
	// MustChangePassword is true when the operator is still on a
	// system-generated temp password (#16); the client routes into the
	// set-new-password flow before anything else.
	MustChangePassword bool `json:"must_change_password"`
}

type FirstRunHandler struct {
	svc   Service
	audit audit.Writer
}

func NewFirstRun(svc Service, auditW audit.Writer) *FirstRunHandler {
	return &FirstRunHandler{svc: svc, audit: auditW}
}

// InitChecker reports whether the system has at least one operator. The
// dashboard polls GET /auth/first-run on load to decide whether to route
// the visitor to the first-run claim page vs. the login page.
type InitChecker interface {
	Initialized(ctx context.Context) (bool, error)
}

// Compile-time check that *authn.AuthN satisfies InitChecker — keeps a
// future refactor of AuthN.Initialized from silently breaking the GET
// handler.
var _ InitChecker = (*authn.AuthN)(nil)

// FirstRunStatusHandler serves GET /auth/first-run. It is the read
// counterpart of the POST claim endpoint and is intentionally
// unauthenticated — the dashboard needs to call it before the operator
// has any tokens.
type FirstRunStatusHandler struct {
	chk InitChecker
}

func NewFirstRunStatus(chk InitChecker) *FirstRunStatusHandler {
	return &FirstRunStatusHandler{chk: chk}
}

type firstRunStatusResponse struct {
	Initialized bool `json:"initialized"`
}

func (h *FirstRunStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	initialized, err := h.chk.Initialized(r.Context())
	if err != nil {
		log.Error("auth.first_run_status", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(firstRunStatusResponse{Initialized: initialized})
}

type firstRunRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *FirstRunHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	var req firstRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	tokens, err := h.svc.ClaimFirstRunAdmin(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, authn.ErrSystemAlreadyInitialized) {
			_ = h.audit.Write(r.Context(), audit.Entry{
				Action:    "audit.first_run",
				ActorType: audit.ActorOperator,
				Outcome:   "denied",
				SourceIP:  clientIP(r),
				UserAgent: r.UserAgent(),
				Payload:   map[string]any{"email": req.Email, "reason": "already_initialized"},
			})
			http.Error(w, "system already initialized", http.StatusGone)
			return
		}
		log.Error("audit.first_run", "outcome", "error", "email", req.Email, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.first_run",
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		Payload:   map[string]any{"email": req.Email},
	})

	writeTokens(w, http.StatusCreated, tokens)
}

// LoginHandler serves POST /auth/login.
type LoginHandler struct {
	svc   Service
	audit audit.Writer
}

func NewLogin(svc Service, auditW audit.Writer) *LoginHandler {
	return &LoginHandler{svc: svc, audit: auditW}
}

type loginRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	TOTPCode     string `json:"totp_code"`
	RecoveryCode string `json:"recovery_code"`
}

func (h *LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	result, err := h.svc.Login(r.Context(), authn.LoginInput{
		Email:        req.Email,
		Password:     req.Password,
		TOTPCode:     req.TOTPCode,
		RecoveryCode: req.RecoveryCode,
	})
	if err != nil {
		switch {
		case errors.Is(err, authn.ErrInvalidCredentials):
			h.writeAudit(r, "failure", "invalid_credentials", req.Email, nil)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		case errors.Is(err, authn.ErrInvalidTotp):
			h.writeAudit(r, "failure", "invalid_totp", req.Email, nil)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		case errors.Is(err, authn.ErrAccountLocked):
			h.writeAudit(r, "failure", "account_locked", req.Email, nil)
			http.Error(w, "account locked", http.StatusTooManyRequests)
			return
		}
		// Unrecognised error — log at error level (no audit row for system
		// errors yet; the future audit-log surface may revisit this).
		log.Error("audit.login", "outcome", "error", "email", req.Email, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.writeAudit(r, "success", "", req.Email, nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(loginResponse{
		AccessToken:            result.Tokens.AccessToken,
		RefreshToken:           result.Tokens.RefreshToken,
		RequiresTotpEnrollment: result.RequiresTotpEnrollment,
		MustChangePassword:     result.MustChangePassword,
	})
}

// writeAudit emits the audit.login entry. The handler holds onto the email
// as the actor hint (the operator id is not bound until after Login succeeds)
// and threads it through the Payload so the legacy slog "email" attr stays.
func (h *LoginHandler) writeAudit(r *http.Request, outcome, reason, email string, extra map[string]any) {
	payload := map[string]any{"email": email}
	if reason != "" {
		payload["reason"] = reason
	}
	for k, v := range extra {
		payload[k] = v
	}
	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.login",
		ActorType: audit.ActorOperator,
		Outcome:   outcome,
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		Payload:   payload,
	})
}

// RefreshHandler serves POST /auth/refresh.
type RefreshHandler struct {
	svc   Service
	audit audit.Writer
}

func NewRefresh(svc Service, auditW audit.Writer) *RefreshHandler {
	return &RefreshHandler{svc: svc, audit: auditW}
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *RefreshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	tokens, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, authn.ErrInvalidRefreshToken) {
			_ = h.audit.Write(r.Context(), audit.Entry{
				Action:    "audit.refresh",
				ActorType: audit.ActorOperator,
				Outcome:   "failure",
				SourceIP:  clientIP(r),
				UserAgent: r.UserAgent(),
				Payload:   map[string]any{"reason": "invalid_refresh_token"},
			})
			http.Error(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		log.Error("audit.refresh", "outcome", "error", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.refresh",
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
	})

	writeTokens(w, http.StatusOK, tokens)
}

// LogoutHandler serves POST /auth/logout. It revokes the presented refresh
// token so a leaked pair cannot rotate forward after Sign out. The endpoint
// is unauthenticated — the refresh token itself is the credential, same as
// /auth/refresh — and intentionally returns 204 for both real revocations
// and unknown tokens so callers cannot probe refresh-token validity.
type LogoutHandler struct {
	svc   Service
	audit audit.Writer
}

func NewLogout(svc Service, auditW audit.Writer) *LogoutHandler {
	return &LogoutHandler{svc: svc, audit: auditW}
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *LogoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	var req logoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		log.Error("audit.logout", "outcome", "error", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.logout",
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
	})

	w.WriteHeader(http.StatusNoContent)
}

// TotpEnroller is the AuthN surface the enrollment handler needs.
type TotpEnroller interface {
	EnrollTotp(ctx context.Context, operatorID string) (authn.TotpEnrollment, error)
}

// TotpEnrollHandler serves POST /auth/totp/enroll. It runs behind the Auth
// middleware, so the operator identity is read from the request context.
type TotpEnrollHandler struct {
	svc   TotpEnroller
	audit audit.Writer
}

func NewTotpEnroll(svc TotpEnroller, auditW audit.Writer) *TotpEnrollHandler {
	return &TotpEnrollHandler{svc: svc, audit: auditW}
}

type totpEnrollResponse struct {
	ProvisioningURI string   `json:"provisioning_uri"`
	RecoveryCodes   []string `json:"recovery_codes"`
}

func (h *TotpEnrollHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	claims, ok := middleware.OperatorFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	enrollment, err := h.svc.EnrollTotp(r.Context(), claims.OperatorID)
	if err != nil {
		if errors.Is(err, authn.ErrTotpAlreadyEnrolled) {
			_ = h.audit.Write(r.Context(), audit.Entry{
				Action:    "audit.totp_enroll",
				ActorID:   claims.OperatorID,
				ActorType: audit.ActorOperator,
				Outcome:   "denied",
				SourceIP:  clientIP(r),
				UserAgent: r.UserAgent(),
				Payload:   map[string]any{"operator_id": claims.OperatorID, "reason": "already_enrolled"},
			})
			http.Error(w, "totp already enrolled", http.StatusConflict)
			return
		}
		log.Error("audit.totp_enroll", "outcome", "error", "operator_id", claims.OperatorID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.totp_enroll",
		ActorID:   claims.OperatorID,
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		Payload:   map[string]any{"operator_id": claims.OperatorID},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(totpEnrollResponse{
		ProvisioningURI: enrollment.ProvisioningURI,
		RecoveryCodes:   enrollment.RecoveryCodes,
	})
}

// writeTokens emits the standard {access_token, refresh_token} JSON body.
func writeTokens(w http.ResponseWriter, status int, tokens authn.Tokens) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(tokensResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	})
}
