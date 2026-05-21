package cplog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

func TestFromContextLoggerCarriesCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cplog.FromContext(r.Context()).Info("handler ran")
		w.WriteHeader(http.StatusOK)
	})
	mw := cplog.Middleware(base)(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Correlation-Id", "test-corr-xyz")
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	var line map[string]any
	if err := json.NewDecoder(&buf).Decode(&line); err != nil {
		t.Fatalf("decode log line: %v", err)
	}
	if got := line["correlation_id"]; got != "test-corr-xyz" {
		t.Errorf("correlation_id: got %v want test-corr-xyz", got)
	}
	if got := line["msg"]; got != "handler ran" {
		t.Errorf("msg: got %v want %q", got, "handler ran")
	}
}

func TestFromContextWithoutMiddlewareFallsBackToDefault(t *testing.T) {
	logger := cplog.FromContext(t.Context())
	if logger == nil {
		t.Fatal("FromContext returned nil; expected slog.Default fallback")
	}
}
