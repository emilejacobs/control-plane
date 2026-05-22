package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/authz"
)

func TestScopeMiddlewareInjectsStaffFilter(t *testing.T) {
	var seen authz.SiteFilter
	var sawIt bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, sawIt = authz.ScopeFromContext(r.Context())
	})

	// A staff operator's scope resolves with no DB, so authz.New(nil) is fine.
	h := Scope(authz.New(nil))(inner)

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	// Simulate the post-Auth state: operator claims already in context.
	ctx := context.WithValue(req.Context(), operatorCtxKey{},
		authn.TokenClaims{OperatorID: "00000000-0000-0000-0000-0000000000aa", IsStaff: true})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))

	if !sawIt {
		t.Fatal("handler saw no SiteFilter in request context")
	}
	if !seen.All {
		t.Errorf("staff operator: injected SiteFilter.All = false, want true")
	}
}
