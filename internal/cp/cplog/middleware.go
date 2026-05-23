// Package cplog is the shared logging primitive for CP Go services.
//
// Per ADR-011 every request crosses several processes and is debugged by
// correlating log lines by a single id. The Middleware extracts the inbound
// X-Correlation-Id header (or generates one when absent), stuffs a logger
// pre-bound with that id into the request context, echoes the id on the
// response, and emits a single access-log line per request on completion.
// Handlers retrieve the request-scoped logger via FromContext.
package cplog

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const HeaderName = "X-Correlation-Id"

// Middleware returns the cplog HTTP middleware. Pass nil for base to use
// slog.Default(); pass a configured logger to inherit its handler + fields.
func Middleware(base *slog.Logger) func(http.Handler) http.Handler {
	if base == nil {
		base = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(HeaderName)
			if id == "" {
				id = uuid.NewString()
			}
			w.Header().Set(HeaderName, id)

			scoped := base.With("correlation_id", id)
			ctx := WithLogger(r.Context(), scoped)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r.WithContext(ctx))

			scoped.Info("request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// statusRecorder shadows the wrapped ResponseWriter so the middleware can
// log the final status code. Default to 200 since a handler that writes
// the body without calling WriteHeader implicitly returns 200.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}
