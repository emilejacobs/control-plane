// Package operators serves the staff-only /operators management endpoints
// (issue #16): list / view / create / edit / deactivate the local-credential
// accounts coworkers log in with. Every route sits behind auth + TOTP + the
// staff gate; mutating routes write an audit entry. The handlers are thin —
// the operators.Store holds the logic.
package operators

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	ops "github.com/emilejacobs/control-plane/internal/cp/operators"
)

// Store is the operator-management surface the handlers depend on.
// *operators.Store satisfies it.
type Store interface {
	List(ctx context.Context) ([]ops.Operator, error)
	Get(ctx context.Context, id string) (ops.Operator, error)
	Create(ctx context.Context, in ops.CreateInput) (ops.CreateResult, error)
	Update(ctx context.Context, id string, in ops.UpdateInput) (ops.Operator, error)
	ResetPassword(ctx context.Context, id string) (string, error)
	SetActive(ctx context.Context, id string, active bool) error
}

// operatorJSON is the wire projection of one operator. site_ids is always an
// array (never null) so the UI can map over it unconditionally.
type operatorJSON struct {
	ID           string   `json:"id"`
	Email        string   `json:"email"`
	IsStaff      bool     `json:"is_staff"`
	TotpEnrolled bool     `json:"totp_enrolled"`
	Deactivated  bool     `json:"deactivated"`
	SiteIDs      []string `json:"site_ids"`
}

func toJSON(o ops.Operator) operatorJSON {
	sites := o.SiteIDs
	if sites == nil {
		sites = []string{}
	}
	return operatorJSON{
		ID: o.ID, Email: o.Email, IsStaff: o.IsStaff,
		TotpEnrolled: o.TotpEnrolled, Deactivated: o.Deactivated, SiteIDs: sites,
	}
}

// --- List ---

type ListHandler struct{ store Store }

func NewList(store Store) *ListHandler { return &ListHandler{store} }

func (h *ListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]operatorJSON, 0, len(list))
	for _, o := range list {
		items = append(items, toJSON(o))
	}
	writeJSON(w, http.StatusOK, map[string]any{"operators": items})
}

// --- Get ---

type GetHandler struct{ store Store }

func NewGet(store Store) *GetHandler { return &GetHandler{store} }

func (h *GetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o, err := h.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, ops.ErrNotFound) {
		http.Error(w, "operator not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toJSON(o))
}

// --- Create ---

type CreateHandler struct {
	store Store
	audit audit.Writer
}

func NewCreate(store Store, auditW audit.Writer) *CreateHandler {
	return &CreateHandler{store, auditW}
}

type createRequest struct {
	Email   string   `json:"email"`
	IsStaff bool     `json:"is_staff"`
	SiteIDs []string `json:"site_ids"`
}

func (h *CreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	res, err := h.store.Create(r.Context(), ops.CreateInput{
		Email: req.Email, IsStaff: req.IsStaff, SiteIDs: req.SiteIDs,
	})
	if errors.Is(err, ops.ErrEmailTaken) {
		h.write(r, "", "error", req.Email)
		http.Error(w, "email already in use", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.write(r, res.Operator.ID, "success", req.Email)
	writeJSON(w, http.StatusCreated, map[string]any{
		"operator":      toJSON(res.Operator),
		"temp_password": res.TempPassword,
	})
}

func (h *CreateHandler) write(r *http.Request, resourceID, outcome, email string) {
	writeAudit(r, h.audit, "audit.operator_create", resourceID, outcome, map[string]any{"email": email})
}

// --- Update ---

type UpdateHandler struct {
	store Store
	audit audit.Writer
}

func NewUpdate(store Store, auditW audit.Writer) *UpdateHandler {
	return &UpdateHandler{store, auditW}
}

type updateRequest struct {
	IsStaff       *bool     `json:"is_staff"`
	SiteIDs       *[]string `json:"site_ids"`
	ResetTotp     bool      `json:"reset_totp"`
	ResetPassword bool      `json:"reset_password"`
}

func (h *UpdateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	o, err := h.store.Update(r.Context(), id, ops.UpdateInput{
		IsStaff: req.IsStaff, SiteIDs: req.SiteIDs, ResetTotp: req.ResetTotp,
	})
	if errors.Is(err, ops.ErrNotFound) {
		http.Error(w, "operator not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]any{"operator": toJSON(o)}
	if req.ResetPassword {
		temp, err := h.store.ResetPassword(r.Context(), id)
		if errors.Is(err, ops.ErrNotFound) {
			http.Error(w, "operator not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp["temp_password"] = temp
	}
	writeAudit(r, h.audit, "audit.operator_update", id, "success", map[string]any{
		"is_staff": req.IsStaff, "reset_totp": req.ResetTotp, "reset_password": req.ResetPassword,
	})
	writeJSON(w, http.StatusOK, resp)
}

// --- SetActive (deactivate / reactivate) ---

type SetActiveHandler struct {
	store  Store
	audit  audit.Writer
	active bool
}

func NewSetActive(store Store, auditW audit.Writer, active bool) *SetActiveHandler {
	return &SetActiveHandler{store, auditW, active}
}

func (h *SetActiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := h.store.SetActive(r.Context(), id, h.active)
	action := "audit.operator_deactivate"
	if h.active {
		action = "audit.operator_reactivate"
	}
	if errors.Is(err, ops.ErrNotFound) {
		http.Error(w, "operator not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeAudit(r, h.audit, action, id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- shared helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAudit(r *http.Request, auditW audit.Writer, action, resourceID, outcome string, payload map[string]any) {
	if auditW == nil {
		return
	}
	claims, _ := middleware.OperatorFromContext(r.Context())
	_ = auditW.Write(r.Context(), audit.Entry{
		Action:       action,
		ActorID:      claims.OperatorID,
		ActorType:    audit.ActorOperator,
		ResourceKind: "operator",
		ResourceID:   resourceID,
		Outcome:      outcome,
		SourceIP:     clientIP(r),
		UserAgent:    r.UserAgent(),
		Payload:      payload,
	})
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
