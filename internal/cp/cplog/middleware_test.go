package cplog_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

func TestCorrelationMiddlewareEchoesIncomingHeader(t *testing.T) {
	mw := cplog.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Correlation-Id", "incoming-abc-123")
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Correlation-Id"); got != "incoming-abc-123" {
		t.Errorf("X-Correlation-Id: got %q want %q", got, "incoming-abc-123")
	}
}

func TestCorrelationMiddlewareGeneratesWhenAbsent(t *testing.T) {
	mw := cplog.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Correlation-Id")
	if got == "" {
		t.Fatal("X-Correlation-Id missing on response; expected a generated value")
	}
	// UUIDv4 is 36 chars (8-4-4-4-12 with hyphens). Don't pin the exact
	// format beyond "non-empty and looks ID-shaped" — picking a generator
	// is the package's internal choice, not part of the public contract.
	if len(got) < 16 {
		t.Errorf("generated X-Correlation-Id too short: %q", got)
	}
}
