package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/audit"
)

// TestHTTPMiddlewareAutoWritesEnvelopeWhenHandlerSkipsAudit locks the
// fall-through behavior: a mutating route whose handler does NOT call
// audit.Writer still produces a row. The row's action encodes the method
// + path so a forgotten audit call surfaces in the table rather than
// going silent.
func TestHTTPMiddlewareAutoWritesEnvelopeWhenHandlerSkipsAudit(t *testing.T) {
	mem := &audit.MemoryWriter{}
	silent := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent) // no audit.Write call
	})
	mw := audit.HTTPMiddleware(mem)(silent)

	req := httptest.NewRequest(http.MethodPost, "/devices/dev-1/commands", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	entries := mem.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1; the middleware should have auto-written one envelope", len(entries))
	}
	if !strings.HasPrefix(entries[0].Action, "audit.http.") {
		t.Errorf("Action: got %q, want prefix %q", entries[0].Action, "audit.http.")
	}
	if entries[0].Payload["method"] != "POST" {
		t.Errorf("Payload[method]: got %v, want POST", entries[0].Payload["method"])
	}
	if entries[0].Payload["path"] != "/devices/dev-1/commands" {
		t.Errorf("Payload[path]: got %v", entries[0].Payload["path"])
	}
	if entries[0].Payload["status"] != 204 {
		t.Errorf("Payload[status]: got %v, want 204", entries[0].Payload["status"])
	}
}

// TestHTTPMiddlewareSuppressesEnvelopeWhenHandlerWrote locks the
// suppression: a handler that calls audit.Write itself produces exactly
// one row — the rich handler-authored entry — not two. Otherwise every
// login produces both an "audit.login" row and an "audit.http.post"
// row, and the table doubles for no signal gain.
func TestHTTPMiddlewareSuppressesEnvelopeWhenHandlerWrote(t *testing.T) {
	mem := &audit.MemoryWriter{}
	handlerWrites := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = mem.Write(r.Context(), audit.Entry{
			Action: "audit.login", Outcome: "success",
		})
		w.WriteHeader(http.StatusOK)
	})
	mw := audit.HTTPMiddleware(mem)(handlerWrites)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	entries := mem.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1; the handler audited, so the middleware envelope should be suppressed", len(entries))
	}
	if entries[0].Action != "audit.login" {
		t.Errorf("Action: got %q, want the handler's rich entry", entries[0].Action)
	}
}

// TestHTTPMiddlewareSkipsReadMethods locks the scope: GET/HEAD/OPTIONS
// are read-side, do not mutate state, and do not produce audit rows.
// Otherwise every dashboard poll inflates audit_log by ~10/sec/operator.
func TestHTTPMiddlewareSkipsReadMethods(t *testing.T) {
	mem := &audit.MemoryWriter{}
	mw := audit.HTTPMiddleware(mem)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/devices", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
	}

	if n := len(mem.Entries()); n != 0 {
		t.Errorf("entries from read methods: got %d, want 0", n)
	}
}

// Silence the unused-import linter when this file is built standalone.
var _ context.Context = context.Background()
