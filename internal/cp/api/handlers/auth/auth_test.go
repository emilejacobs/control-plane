package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/auth"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

type fakeAuth struct{ loginErr error }

func (f fakeAuth) ClaimFirstRunAdmin(context.Context, string, string) (authn.Tokens, error) {
	return authn.Tokens{}, nil
}

func (f fakeAuth) Login(context.Context, authn.LoginInput) (authn.LoginResult, error) {
	return authn.LoginResult{}, f.loginErr
}

func (f fakeAuth) Refresh(context.Context, string) (authn.Tokens, error) {
	return authn.Tokens{}, nil
}

// TestLoginFailureWritesAuditEntry locks in the cycle-3 behavior: a login
// that fails ErrInvalidCredentials writes an audit Entry through the
// injected Writer with action=audit.login, outcome=failure, and the email
// + reason in the payload. The integration test will continue to assert
// the slog line shape; this unit test asserts the structured Writer call.
func TestLoginFailureWritesAuditEntry(t *testing.T) {
	mem := &audit.MemoryWriter{}
	h := auth.NewLogin(fakeAuth{loginErr: authn.ErrInvalidCredentials}, mem)

	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"op@example.com","password":"x","totp_code":"000000"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", rec.Code, rec.Body.String())
	}

	entries := mem.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Action != "audit.login" {
		t.Errorf("Action: got %q, want %q", e.Action, "audit.login")
	}
	if e.Outcome != "failure" {
		t.Errorf("Outcome: got %q, want %q", e.Outcome, "failure")
	}
	if e.Payload["reason"] != "invalid_credentials" {
		t.Errorf("Payload[reason]: got %v, want %q", e.Payload["reason"], "invalid_credentials")
	}
	if e.Payload["email"] != "op@example.com" {
		t.Errorf("Payload[email]: got %v, want %q", e.Payload["email"], "op@example.com")
	}
}

// Compile-time guard that authn.ErrInvalidCredentials still exists; if the
// authn package renames it, this test fails fast at build time rather than
// at runtime with a confusing assertion miss.
var _ = errors.Is(authn.ErrInvalidCredentials, authn.ErrInvalidCredentials)

// fakeInitChecker is the test double for auth.InitChecker. The dashboard
// uses GET /auth/first-run to decide whether to route the operator to the
// first-run page; the handler delegates the decision to InitChecker.
type fakeInitChecker struct {
	initialized bool
	err         error
}

func (f fakeInitChecker) Initialized(context.Context) (bool, error) {
	return f.initialized, f.err
}

func TestFirstRunStatusReturnsUninitialized(t *testing.T) {
	h := auth.NewFirstRunStatus(fakeInitChecker{initialized: false})

	req := httptest.NewRequest(http.MethodGet, "/auth/first-run", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", got)
	}
	want := `"initialized":false`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("body: got %q, want substring %q", rec.Body.String(), want)
	}
}

func TestFirstRunStatusReturnsInitialized(t *testing.T) {
	h := auth.NewFirstRunStatus(fakeInitChecker{initialized: true})

	req := httptest.NewRequest(http.MethodGet, "/auth/first-run", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `"initialized":true`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("body: got %q, want substring %q", rec.Body.String(), want)
	}
}

func TestFirstRunStatusPropagatesDbError(t *testing.T) {
	h := auth.NewFirstRunStatus(fakeInitChecker{err: errors.New("db down")})

	req := httptest.NewRequest(http.MethodGet, "/auth/first-run", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}
