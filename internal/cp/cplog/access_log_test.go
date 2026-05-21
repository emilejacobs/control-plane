package cplog_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

func TestMiddlewareLogsRequestCompleted(t *testing.T) {
	var buf bytes.Buffer
	base := cplog.New(&buf, "cp-api-test")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	mw := cplog.Middleware(base)(handler)

	req := httptest.NewRequest(http.MethodGet, "/widgets/42", nil)
	req.Header.Set("X-Correlation-Id", "test-corr-access")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	// Exactly one line should be on the buffer — the request-completed
	// access-log line. (The handler in this test doesn't emit its own.)
	var line map[string]any
	if err := json.NewDecoder(&buf).Decode(&line); err != nil {
		t.Fatalf("decode log line: %v; buf=%q", err, buf.String())
	}

	if got := line["msg"]; got != "request completed" {
		t.Errorf("msg: got %v want %q", got, "request completed")
	}
	if got := line["method"]; got != http.MethodGet {
		t.Errorf("method: got %v want GET", got)
	}
	if got := line["path"]; got != "/widgets/42" {
		t.Errorf("path: got %v want /widgets/42", got)
	}
	if got := line["status"]; got != float64(http.StatusTeapot) {
		t.Errorf("status: got %v want %d", got, http.StatusTeapot)
	}
	if got := line["correlation_id"]; got != "test-corr-access" {
		t.Errorf("correlation_id: got %v want test-corr-access", got)
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Errorf("duration_ms missing; got keys=%v", line)
	}
	if got := line["service"]; got != "cp-api-test" {
		t.Errorf("service: got %v want cp-api-test", got)
	}
}
