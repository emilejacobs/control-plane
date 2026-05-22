// Package auth serves /auth/* endpoints: first-run admin bootstrap,
// login, refresh, and TOTP enrollment.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

type Service interface {
	ClaimFirstRunAdmin(ctx context.Context, email, password string) (authn.Tokens, error)
	Login(ctx context.Context, email, password string) (authn.Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (authn.Tokens, error)
}

type tokensResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type FirstRunHandler struct {
	svc Service
}

func NewFirstRun(svc Service) *FirstRunHandler { return &FirstRunHandler{svc: svc} }

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
			log.Info("audit.first_run", "outcome", "denied", "reason", "already_initialized", "email", req.Email)
			http.Error(w, "system already initialized", http.StatusGone)
			return
		}
		log.Error("audit.first_run", "outcome", "error", "email", req.Email, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Info("audit.first_run", "outcome", "success", "email", req.Email)

	writeTokens(w, http.StatusCreated, tokens)
}

// LoginHandler serves POST /auth/login.
type LoginHandler struct {
	svc Service
}

func NewLogin(svc Service) *LoginHandler { return &LoginHandler{svc: svc} }

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	tokens, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, authn.ErrInvalidCredentials) {
			log.Info("audit.login", "outcome", "failure", "reason", "invalid_credentials", "email", req.Email)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		if errors.Is(err, authn.ErrAccountLocked) {
			log.Info("audit.login", "outcome", "failure", "reason", "account_locked", "email", req.Email)
			http.Error(w, "account locked", http.StatusTooManyRequests)
			return
		}
		log.Error("audit.login", "outcome", "error", "email", req.Email, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Info("audit.login", "outcome", "success", "email", req.Email)

	writeTokens(w, http.StatusOK, tokens)
}

// RefreshHandler serves POST /auth/refresh.
type RefreshHandler struct {
	svc Service
}

func NewRefresh(svc Service) *RefreshHandler { return &RefreshHandler{svc: svc} }

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
			log.Info("audit.refresh", "outcome", "failure", "reason", "invalid_refresh_token")
			http.Error(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		log.Error("audit.refresh", "outcome", "error", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Info("audit.refresh", "outcome", "success")

	writeTokens(w, http.StatusOK, tokens)
}

// TotpEnroller is the AuthN surface the enrollment handler needs.
type TotpEnroller interface {
	EnrollTotp(ctx context.Context, operatorID string) (authn.TotpEnrollment, error)
}

// TotpEnrollHandler serves POST /auth/totp/enroll. It runs behind the Auth
// middleware, so the operator identity is read from the request context.
type TotpEnrollHandler struct {
	svc TotpEnroller
}

func NewTotpEnroll(svc TotpEnroller) *TotpEnrollHandler { return &TotpEnrollHandler{svc: svc} }

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
			log.Info("audit.totp_enroll", "outcome", "denied", "reason", "already_enrolled", "operator_id", claims.OperatorID)
			http.Error(w, "totp already enrolled", http.StatusConflict)
			return
		}
		log.Error("audit.totp_enroll", "outcome", "error", "operator_id", claims.OperatorID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Info("audit.totp_enroll", "outcome", "success", "operator_id", claims.OperatorID)

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
