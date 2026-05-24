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

type fakeAuth struct {
	loginErr     error
	logoutCalled string
	logoutErr    error
}

func (f *fakeAuth) ClaimFirstRunAdmin(context.Context, string, string) (authn.Tokens, error) {
	return authn.Tokens{}, nil
}

func (f *fakeAuth) Login(context.Context, authn.LoginInput) (authn.LoginResult, error) {
	return authn.LoginResult{}, f.loginErr
}

func (f *fakeAuth) Refresh(context.Context, string) (authn.Tokens, error) {
	return authn.Tokens{}, nil
}

func (f *fakeAuth) Logout(_ context.Context, refreshToken string) error {
	f.logoutCalled = refreshToken
	return f.logoutErr
}

// TestLoginFailureWritesAuditEntry locks in the cycle-3 behavior: a login
// that fails ErrInvalidCredentials writes an audit Entry through the
// injected Writer with action=audit.login, outcome=failure, and the email
// + reason in the payload. The integration test will continue to assert
// the slog line shape; this unit test asserts the structured Writer call.
func TestLoginFailureWritesAuditEntry(t *testing.T) {
	mem := &audit.MemoryWriter{}
	h := auth.NewLogin(&fakeAuth{loginErr: authn.ErrInvalidCredentials}, mem)

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

// TestLogoutRevokesRefreshTokenAndAudits locks in the contract for
// POST /auth/logout: the handler revokes the presented refresh token via
// the AuthN service, returns 204 No Content, and writes an audit.logout
// entry. The endpoint deliberately does not surface whether the token was
// actually valid — returning 204 either way avoids leaking token state.
func TestLogoutRevokesRefreshTokenAndAudits(t *testing.T) {
	mem := &audit.MemoryWriter{}
	fa := &fakeAuth{}
	h := auth.NewLogout(fa, mem)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout",
		strings.NewReader(`{"refresh_token":"refresh-abc"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if fa.logoutCalled != "refresh-abc" {
		t.Errorf("Logout: called with %q, want %q", fa.logoutCalled, "refresh-abc")
	}

	entries := mem.Entries()
	if len(entries) != 1 {
		t.Fatalf("audit entries: got %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Action != "audit.logout" {
		t.Errorf("Action: got %q, want %q", e.Action, "audit.logout")
	}
	if e.Outcome != "success" {
		t.Errorf("Outcome: got %q, want %q", e.Outcome, "success")
	}
}

// TestLogoutStillReturns204WhenTokenIsUnknown is the privacy contract:
// the handler does not leak refresh-token validity. authn.Logout returns
// nil for unknown tokens; the handler returns 204 like it does for a real
// revocation. The audit row carries outcome=success — from cp-api's POV
// the call was well-formed.
func TestLogoutStillReturns204WhenTokenIsUnknown(t *testing.T) {
	mem := &audit.MemoryWriter{}
	fa := &fakeAuth{}
	h := auth.NewLogout(fa, mem)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout",
		strings.NewReader(`{"refresh_token":"never-issued"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", rec.Code)
	}
}

// TestLogoutRejectsInvalidJSON ensures the handler does not 500 on
// malformed input — symmetrical with the other auth handlers.
func TestLogoutRejectsInvalidJSON(t *testing.T) {
	h := auth.NewLogout(&fakeAuth{}, &audit.MemoryWriter{})

	req := httptest.NewRequest(http.MethodPost, "/auth/logout",
		strings.NewReader(`{not-json`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
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
