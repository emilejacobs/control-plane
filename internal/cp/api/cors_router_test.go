package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api"
)

// TestRouterAnswersCorsPreflightOnAuthFirstRun is the bug-driven test:
// without CORS wiring, OPTIONS /auth/first-run 405s because the mux only
// has POST registered, and the dashboard's first-run claim fails with
// "Could not create the account."
func TestRouterAnswersCorsPreflightOnAuthFirstRun(t *testing.T) {
	h := api.NewRouter(api.Deps{
		CORSAllowedOrigins: []string{"https://control.uknomi.com"},
	})

	req := httptest.NewRequest(http.MethodOptions, "/auth/first-run", nil)
	req.Header.Set("Origin", "https://control.uknomi.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,idempotency-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://control.uknomi.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want https://control.uknomi.com", got)
	}
}

// TestRouterAddsCorsHeadersToHealthz proves the wrapping covers every
// route, not just /auth/*.
func TestRouterAddsCorsHeadersToHealthz(t *testing.T) {
	h := api.NewRouter(api.Deps{
		CORSAllowedOrigins: []string{"https://control.uknomi.com"},
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://control.uknomi.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://control.uknomi.com" {
		t.Errorf("Allow-Origin = %q, want https://control.uknomi.com", got)
	}
}

// TestRouterEmptyAllowedOriginsIsBackwardsCompatible verifies that the
// existing zero-value Deps usage (in healthz_test and elsewhere) keeps
// working — no CORS headers and no preflight handling.
func TestRouterEmptyAllowedOriginsIsBackwardsCompatible(t *testing.T) {
	h := api.NewRouter(api.Deps{}) // no CORSAllowedOrigins

	// Healthz still 200 + no CORS headers when allow list is empty.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://control.uknomi.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin must be absent when CORSAllowedOrigins is empty, got %q", got)
	}
}
