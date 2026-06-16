// Package settings serves the staff-only CP-singleton settings surface
// (#84) — currently the account-wide Plate Recognizer token. Secret values are
// write-only over the API: PUT sets, GET reports only whether a value is set.
package settings

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// Store is the registry surface these handlers use.
type Store interface {
	SetCPSetting(ctx context.Context, key, value string) error
	GetCPSetting(ctx context.Context, key string) (string, bool, error)
}

type isSetResponse struct {
	IsSet bool `json:"is_set"`
}

// PRTokenGetHandler serves GET /settings/pr-token — reports only whether the
// account-wide PR token is set, never the value itself.
type PRTokenGetHandler struct{ store Store }

func NewPRTokenGet(store Store) *PRTokenGetHandler { return &PRTokenGetHandler{store: store} }

func (h *PRTokenGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	val, ok, err := h.store.GetCPSetting(r.Context(), registry.SettingPlateRecognizerToken)
	if err != nil {
		cplog.FromContext(r.Context()).Error("get pr token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, isSetResponse{IsSet: ok && val != ""})
}

// PRTokenPutHandler serves PUT /settings/pr-token — staff sets the account-wide
// PR token. The token is a secret: kept out of the audit payload and logs.
type PRTokenPutHandler struct {
	store Store
	audit audit.Writer
}

func NewPRTokenPut(store Store, auditW audit.Writer) *PRTokenPutHandler {
	return &PRTokenPutHandler{store: store, audit: auditW}
}

type prTokenRequest struct {
	Token string `json:"token"`
}

func (h *PRTokenPutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := cplog.FromContext(r.Context())
	claims, _ := middleware.OperatorFromContext(r.Context()) // staff-gate guaranteed

	var req prTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Token == "" {
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}

	if err := h.store.SetCPSetting(r.Context(), registry.SettingPlateRecognizerToken, req.Token); err != nil {
		log.Error("audit.pr_token", "outcome", "error", "err", err)
		_ = h.audit.Write(r.Context(), audit.Entry{
			Action:    "audit.pr_token",
			ActorID:   claims.OperatorID,
			ActorType: audit.ActorOperator,
			Outcome:   "error",
			SourceIP:  clientIP(r),
			UserAgent: r.UserAgent(),
			Payload:   map[string]any{"is_set": false},
		})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = h.audit.Write(r.Context(), audit.Entry{
		Action:    "audit.pr_token",
		ActorID:   claims.OperatorID,
		ActorType: audit.ActorOperator,
		Outcome:   "success",
		SourceIP:  clientIP(r),
		UserAgent: r.UserAgent(),
		// No token value in the payload — it is a secret.
		Payload: map[string]any{"is_set": true},
	})
	writeJSON(w, isSetResponse{IsSet: true})
}
