package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
