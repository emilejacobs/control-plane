package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// terminal is a handler that records that it ran and writes a marker body
// so tests can prove the wrapped handler was (or was not) reached.
func terminal(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "inner")
	})
}

func TestCorsAllowsListedOriginOnSimpleRequest(t *testing.T) {
	var reached bool
	h := Cors([]string{"https://control.uknomi.com"})(terminal(&reached))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://control.uknomi.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("inner handler must run for a simple cross-origin request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://control.uknomi.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://control.uknomi.com")
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Origin") {
		t.Errorf("Vary header must include Origin, got %q", vary)
	}
	// The two-step login + gate middleware signal via the custom "Reason"
	// response header; a cross-origin browser can only read it when CORS
	// exposes it. Without this, enrolled operators can't reach the 2FA step.
	if got := rec.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Reason") {
		t.Errorf("Access-Control-Expose-Headers = %q, want it to include Reason", got)
	}
}

func TestCorsTerminatesPreflightWithoutCallingInner(t *testing.T) {
	var reached bool
	h := Cors([]string{"https://control.uknomi.com"})(terminal(&reached))

	req := httptest.NewRequest(http.MethodOptions, "/auth/first-run", nil)
	req.Header.Set("Origin", "https://control.uknomi.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,idempotency-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if reached {
		t.Fatal("inner handler must NOT run for preflight; the middleware terminates")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://control.uknomi.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://control.uknomi.com")
	}
	allowMethods := rec.Header().Get("Access-Control-Allow-Methods")
	// DELETE is in the list: bench smoke 2026-05-26 caught its absence
	// when CamerasPanel's per-camera delete button surfaced "Method
	// DELETE is not allowed by Access-Control-Allow-Methods" in the
	// browser. Whenever we expose a new HTTP verb at the router, it
	// must show up here too.
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"} {
		if !strings.Contains(allowMethods, m) {
			t.Errorf("Allow-Methods missing %s, got %q", m, allowMethods)
		}
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	for _, h := range []string{"Authorization", "Content-Type", "Idempotency-Key"} {
		if !strings.Contains(allowHeaders, h) {
			t.Errorf("Allow-Headers missing %s, got %q", h, allowHeaders)
		}
	}
}

func TestCorsDoesNotEchoUnlistedOrigin(t *testing.T) {
	var reached bool
	h := Cors([]string{"https://control.uknomi.com"})(terminal(&reached))

	// A browser at an unlisted origin sends Origin; the middleware passes the
	// request through (non-browser clients sending Origin still work) but does
	// NOT add Access-Control-Allow-Origin, so the browser rejects the response.
	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("inner handler must still run; CORS is browser-enforced, not server-rejected")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin must be empty for unlisted origin, got %q", got)
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Origin") {
		t.Errorf("Vary still required so caches don't reuse this response for an allowed origin")
	}
}

func TestCorsRejectsPreflightFromUnlistedOrigin(t *testing.T) {
	var reached bool
	h := Cors([]string{"https://control.uknomi.com"})(terminal(&reached))

	req := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Preflight without Allow-Origin is a CORS failure on the browser side.
	// The server SHOULD NOT call the inner handler (a preflight is not a real
	// request) and should return a non-success status — 403 to make the cause
	// debuggable from the network tab.
	if reached {
		t.Fatal("inner handler must not run for preflight from unlisted origin")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin must be empty for unlisted origin, got %q", got)
	}
}

func TestCorsPassesThroughNonBrowserRequest(t *testing.T) {
	// No Origin header (e.g. curl, server-to-server): no CORS handling at all.
	var reached bool
	h := Cors([]string{"https://control.uknomi.com"})(terminal(&reached))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("inner handler must run for same-origin / non-browser requests")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin must be absent when no Origin header is present, got %q", got)
	}
	if got := rec.Header().Get("Vary"); strings.Contains(got, "Origin") {
		t.Errorf("Vary: Origin should not be added when no Origin header is present (avoids cache fragmentation), got %q", got)
	}
}

func TestCorsEmptyAllowListIsNoOp(t *testing.T) {
	// Disabling CORS via empty list: even with an Origin header, no CORS
	// processing happens. Useful in tests / single-origin deployments.
	var reached bool
	h := Cors(nil)(terminal(&reached))

	req := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
	req.Header.Set("Origin", "https://control.uknomi.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Preflight passes through to the inner handler, which (in the real
	// router) will 405. The point of the test is that the middleware is
	// transparent when the allow list is empty.
	if !reached {
		t.Fatal("with empty allow list, the middleware must be transparent")
	}
}
