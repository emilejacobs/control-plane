package operators

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/audit"
	ops "github.com/emilejacobs/control-plane/internal/cp/operators"
)

type fakeStore struct {
	list        []ops.Operator
	get         ops.Operator
	getErr      error
	createRes   ops.CreateResult
	createErr   error
	updateRes   ops.Operator
	updateErr   error
	resetPw     string
	resetErr    error
	setActiveErr error

	lastCreate   ops.CreateInput
	lastUpdate   ops.UpdateInput
	lastActiveID string
	lastActive   bool
}

func (f *fakeStore) List(context.Context) ([]ops.Operator, error) { return f.list, nil }
func (f *fakeStore) Get(context.Context, string) (ops.Operator, error) {
	return f.get, f.getErr
}
func (f *fakeStore) Create(_ context.Context, in ops.CreateInput) (ops.CreateResult, error) {
	f.lastCreate = in
	return f.createRes, f.createErr
}
func (f *fakeStore) Update(_ context.Context, _ string, in ops.UpdateInput) (ops.Operator, error) {
	f.lastUpdate = in
	return f.updateRes, f.updateErr
}
func (f *fakeStore) ResetPassword(context.Context, string) (string, error) {
	return f.resetPw, f.resetErr
}
func (f *fakeStore) SetActive(_ context.Context, id string, active bool) error {
	f.lastActiveID, f.lastActive = id, active
	return f.setActiveErr
}

func TestListHandler(t *testing.T) {
	store := &fakeStore{list: []ops.Operator{
		{ID: "op-1", Email: "a@x.test", IsStaff: true, TotpEnrolled: true, SiteIDs: []string{}},
		{ID: "op-2", Email: "b@x.test", Deactivated: true, SiteIDs: []string{"site-1"}},
	}}
	rec := httptest.NewRecorder()
	NewList(store).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/operators", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Operators []struct {
			ID           string   `json:"id"`
			Email        string   `json:"email"`
			IsStaff      bool     `json:"is_staff"`
			TotpEnrolled bool     `json:"totp_enrolled"`
			Deactivated  bool     `json:"deactivated"`
			SiteIDs      []string `json:"site_ids"`
		} `json:"operators"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Operators) != 2 || body.Operators[0].Email != "a@x.test" {
		t.Fatalf("operators = %+v", body.Operators)
	}
	if !body.Operators[1].Deactivated || body.Operators[1].SiteIDs[0] != "site-1" {
		t.Errorf("op2 = %+v", body.Operators[1])
	}
}

func TestGetHandlerNotFound(t *testing.T) {
	store := &fakeStore{getErr: ops.ErrNotFound}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/operators/missing", nil)
	req.SetPathValue("id", "missing")
	NewGet(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCreateHandlerReturnsTempPasswordOnce(t *testing.T) {
	aud := &audit.MemoryWriter{}
	store := &fakeStore{createRes: ops.CreateResult{
		Operator:     ops.Operator{ID: "op-9", Email: "new@x.test", SiteIDs: []string{"s1"}},
		TempPassword: "generated-temp-pw",
	}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/operators",
		strings.NewReader(`{"email":"new@x.test","is_staff":false,"site_ids":["s1"]}`))
	NewCreate(store, aud).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Operator     map[string]any `json:"operator"`
		TempPassword string         `json:"temp_password"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TempPassword != "generated-temp-pw" {
		t.Errorf("temp_password = %q", body.TempPassword)
	}
	if store.lastCreate.Email != "new@x.test" {
		t.Errorf("store got email %q", store.lastCreate.Email)
	}
	if len(aud.Entries()) != 1 || aud.Entries()[0].Outcome != "success" {
		t.Errorf("audit entries = %+v, want one success", aud.Entries())
	}
}

func TestCreateHandlerEmailTaken(t *testing.T) {
	store := &fakeStore{createErr: ops.ErrEmailTaken}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/operators", strings.NewReader(`{"email":"dup@x.test"}`))
	NewCreate(store, &audit.MemoryWriter{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestUpdateHandlerResetPasswordReturnsTemp(t *testing.T) {
	store := &fakeStore{updateRes: ops.Operator{ID: "op-3", Email: "e@x.test"}, resetPw: "fresh-temp"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/operators/op-3",
		strings.NewReader(`{"is_staff":true,"reset_password":true}`))
	req.SetPathValue("id", "op-3")
	NewUpdate(store, &audit.MemoryWriter{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body)
	}
	if store.lastUpdate.IsStaff == nil || !*store.lastUpdate.IsStaff {
		t.Errorf("store IsStaff = %v, want true", store.lastUpdate.IsStaff)
	}
	var body struct {
		TempPassword string `json:"temp_password"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.TempPassword != "fresh-temp" {
		t.Errorf("temp_password = %q, want fresh-temp on reset_password", body.TempPassword)
	}
}

func TestSetActiveHandlerDeactivates(t *testing.T) {
	store := &fakeStore{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/operators/op-4/deactivate", nil)
	req.SetPathValue("id", "op-4")
	NewSetActive(store, &audit.MemoryWriter{}, false).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if store.lastActiveID != "op-4" || store.lastActive {
		t.Errorf("SetActive(%q, %v), want (op-4, false)", store.lastActiveID, store.lastActive)
	}
}
