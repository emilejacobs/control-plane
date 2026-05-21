// Package cplog is the shared logging primitive for CP Go services.
//
// Per ADR-011 every request crosses several processes and is debugged by
// correlating log lines by a single id. The Middleware extracts the inbound
// X-Correlation-Id header (or generates one when absent), stuffs a logger
// pre-bound with that id into the request context, and echoes the id on the
// response. Handlers retrieve the logger via FromContext.
package cplog

import (
	"log/slog"
	"net/http"

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
			ctx := withLogger(r.Context(), scoped)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
