package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api"
)

// TestHealthzReturns200 verifies that GET /healthz returns 200 with an empty
// body, no auth required. The ALB target group's health check (ADR-022, #25)
// hits this path; until it returns 200 the ALB matcher has to be widened to
// 200-499, which masks real failures behind a 4xx.
func TestHealthzReturns200(t *testing.T) {
	h := api.NewBuilderWith(api.Deps{}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body: got %q, want empty", rec.Body.String())
	}
}
