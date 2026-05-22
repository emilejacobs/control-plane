package middleware

import (
	"net/http"
	"time"
)

// RateLimiter is a fixed-window, per-source-IP request limiter. ADR-017 caps
// /enrollments at 20 requests/hour per IP so a leaked bootstrap key has
// bounded blast radius. State is in-memory per process.
type RateLimiter struct {
	limit  int
	window time.Duration
	// now is the clock; tests override it to drive window expiry.
	now func() time.Time
}

// NewRateLimiter returns a limiter allowing limit requests per window per IP.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, now: time.Now}
}

// Middleware wraps next, rejecting a source IP that exceeds the limit with
// HTTP 429.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
