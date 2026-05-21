// Package cplog is the shared logging primitive for CP Go services.
//
// Per ADR-011 every request crosses several processes and is debugged by
// correlating log lines by a single id. The Correlation middleware extracts
// the inbound X-Correlation-Id header (or generates one when absent), stuffs
// it into the request context, and echoes it on the response.
package cplog

import (
	"net/http"

	"github.com/google/uuid"
)

const HeaderName = "X-Correlation-Id"

// Correlation returns the middleware. Each request gets a correlation_id
// echoed on its response — incoming header preserved verbatim, fresh UUID
// generated when the header is absent.
func Correlation() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(HeaderName)
			if id == "" {
				id = uuid.NewString()
			}
			w.Header().Set(HeaderName, id)
			next.ServeHTTP(w, r)
		})
	}
}
