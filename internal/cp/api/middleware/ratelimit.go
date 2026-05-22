package middleware

import (
	"net"
	"net/http"
	"sync"
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

	mu      sync.Mutex
	windows map[string]*ipWindow
}

// ipWindow is one source IP's current fixed window: when it opened and how
// many requests have landed in it.
type ipWindow struct {
	start time.Time
	count int
}

// NewRateLimiter returns a limiter allowing limit requests per window per IP.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		windows: make(map[string]*ipWindow),
	}
}

// allow records a request from ip and reports whether it is within the
// limit. The window resets once it has fully elapsed.
func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	w := rl.windows[ip]
	if w == nil || now.Sub(w.start) >= rl.window {
		w = &ipWindow{start: now}
		rl.windows[ip] = w
	}
	w.count++
	return w.count <= rl.limit
}

// Middleware wraps next, rejecting a source IP that exceeds the limit with
// HTTP 429.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r)) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the source address of r without the port.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
