package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

type fakePwChecker struct {
	must bool
	err  error
}

func (f fakePwChecker) MustChangePassword(context.Context, string) (bool, error) {
	return f.must, f.err
}

func withOperator(req *http.Request) *http.Request {
	ctx := context.WithValue(req.Context(), operatorCtxKey{},
		authn.TokenClaims{OperatorID: "00000000-0000-0000-0000-0000000000aa"})
	return req.WithContext(ctx)
}

func TestRequirePasswordChangedBlocksWhenMustChange(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	h := RequirePasswordChanged(fakePwChecker{must: true})(inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withOperator(httptest.NewRequest(http.MethodGet, "/devices", nil)))

	if called {
		t.Error("inner handler ran, want blocked")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("Reason"); got != "password-change-required" {
		t.Errorf("Reason = %q, want password-change-required", got)
	}
}

func TestRequirePasswordChangedAllowsWhenChanged(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := RequirePasswordChanged(fakePwChecker{must: false})(inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withOperator(httptest.NewRequest(http.MethodGet, "/devices", nil)))

	if !called || rec.Code != http.StatusOK {
		t.Errorf("called=%v code=%d, want true/200", called, rec.Code)
	}
}

func TestRequirePasswordChangedUnauthenticated(t *testing.T) {
	h := RequirePasswordChanged(fakePwChecker{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
