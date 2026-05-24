package middleware

import "net/http"

// Cors returns middleware that handles CORS for the configured allow list.
// An empty list disables CORS entirely (transparent passthrough).
//
// The dashboard is served from a sibling subdomain (`control.uknomi.com`)
// while cp-api answers on `api.control.uknomi.com`, so every browser call
// from the dashboard is cross-origin and the browser preflights it. Without
// this middleware the chi mux 405s the preflight and the actual request
// never runs.
//
// Behavior:
//   - No Origin header: passthrough, no CORS headers added (non-browser /
//     same-origin clients are unaffected and caches stay coherent).
//   - Origin not on the allow list, preflight: 403 with no Allow-Origin so
//     the browser surfaces a clear CORS failure in the network tab.
//   - Origin not on the allow list, simple request: passthrough with no
//     Allow-Origin — the response leaves the server, the browser drops it.
//     Server-side rejection would also break non-browser clients that
//     happen to set Origin.
//   - Origin on the allow list, preflight (OPTIONS): 204 with the standard
//     Allow-Origin / Allow-Methods / Allow-Headers / Max-Age and the inner
//     handler is NOT called (the chi router would 405 anyway).
//   - Origin on the allow list, simple request: Allow-Origin echoed and the
//     inner handler runs normally.
func Cors(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	if len(allowed) == 0 {
		// No-op middleware so callers can wire it unconditionally.
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Caches must key by Origin so an allow-listed response
			// is never replayed for a different (unlisted) origin.
			w.Header().Add("Vary", "Origin")

			_, ok := allowed[origin]
			if !ok {
				if r.Method == http.MethodOptions {
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
