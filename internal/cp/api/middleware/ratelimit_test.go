package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

// okHandler is a terminal handler that records it was reached.
func okHandler(reached *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached++
		w.WriteHeader(http.StatusOK)
	})
}

// from issues one request through h from the given source address.
func from(h http.Handler, remoteAddr string) int {
	req := httptest.NewRequest(http.MethodPost, "/enrollments", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	reached := 0
	rl := NewRateLimiter(20, time.Hour)
	h := rl.Middleware(okHandler(&reached))

	if code := from(h, "10.0.0.1:5000"); code != http.StatusOK {
		t.Fatalf("status: got %d want 200", code)
	}
	if reached != 1 {
		t.Errorf("handler reached %d times, want 1", reached)
	}
}

func TestRateLimiterBlocksOverLimit(t *testing.T) {
	reached := 0
	rl := NewRateLimiter(20, time.Hour)
	h := rl.Middleware(okHandler(&reached))

	for i := 0; i < 20; i++ {
		if code := from(h, "10.0.0.1:5000"); code != http.StatusOK {
			t.Fatalf("request %d: got %d want 200", i+1, code)
		}
	}
	// The 21st request in the window is rejected without reaching the
	// handler.
	if code := from(h, "10.0.0.1:5000"); code != http.StatusTooManyRequests {
		t.Fatalf("21st request: got %d want 429", code)
	}
	if reached != 20 {
		t.Errorf("handler reached %d times, want 20", reached)
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	reached := 0
	rl := NewRateLimiter(20, time.Hour)
	clock := time.Now()
	rl.now = func() time.Time { return clock }
	h := rl.Middleware(okHandler(&reached))

	for i := 0; i < 21; i++ {
		from(h, "10.0.0.1:5000")
	}
	// An hour on, the window has fully elapsed — the IP is allowed again.
	clock = clock.Add(time.Hour)
	if code := from(h, "10.0.0.1:5000"); code != http.StatusOK {
		t.Fatalf("request after window: got %d want 200", code)
	}
}

func TestRateLimiterIsolatesByIP(t *testing.T) {
	reached := 0
	rl := NewRateLimiter(20, time.Hour)
	h := rl.Middleware(okHandler(&reached))

	for i := 0; i < 21; i++ {
		from(h, "10.0.0.1:5000")
	}
	// A different source IP has its own window — unaffected by the first.
	if code := from(h, "10.0.0.2:5000"); code != http.StatusOK {
		t.Fatalf("second IP: got %d want 200", code)
	}
}

// TestRateLimiterEmitsTripLogLine locks the Issue 21 alarm signal: when
// the limiter rejects a request, it logs "ratelimit.trip" with the
// source_ip via the cplog request-scoped logger. CloudWatch turns the
// line count into a metric and pages when an IP starts probing the
// enrollment endpoint; the runbook bridges the per-IP precision the
// metric filter cannot express directly.
func TestRateLimiterEmitsTripLogLine(t *testing.T) {
	reached := 0
	rl := NewRateLimiter(2, time.Hour)
	h := rl.Middleware(okHandler(&reached))

	var logbuf bytes.Buffer
	logger := cplog.New(&logbuf, "cp-api-test")
	ctx := cplog.WithLogger(context.Background(), logger)
	fromCtx := func(remoteAddr string) int {
		req := httptest.NewRequest(http.MethodPost, "/enrollments", nil).WithContext(ctx)
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Within the limit — no trip line.
	fromCtx("10.0.0.5:5000")
	fromCtx("10.0.0.5:5000")
	if strings.Contains(logbuf.String(), "ratelimit.trip") {
		t.Fatalf("trip line emitted while still under limit:\n%s", logbuf.String())
	}

	// 3rd request trips the limit and emits exactly one line.
	if code := fromCtx("10.0.0.5:5000"); code != http.StatusTooManyRequests {
		t.Fatalf("3rd request: got %d want 429", code)
	}
	if n := strings.Count(logbuf.String(), `"msg":"ratelimit.trip"`); n != 1 {
		t.Errorf("ratelimit.trip lines: got %d want 1\nbuf:\n%s", n, logbuf.String())
	}
	if !strings.Contains(logbuf.String(), `"source_ip":"10.0.0.5"`) {
		t.Errorf("source_ip not in line:\n%s", logbuf.String())
	}
}
