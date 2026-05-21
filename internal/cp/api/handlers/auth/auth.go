// Package auth serves /auth/* endpoints: first-run admin bootstrap,
// login, refresh. TOTP arrives in Issue 05.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

type Service interface {
	ClaimFirstRunAdmin(ctx context.Context, email, password string) (authn.Tokens, error)
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(tokensResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	})
}
