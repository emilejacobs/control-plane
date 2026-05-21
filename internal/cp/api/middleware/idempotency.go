// Package middleware holds HTTP middlewares applied at router-construction
// time. The idempotency middleware enforces ADR-012's Idempotency-Key
// contract: state-mutating handlers wrapped by Idempotency replay the
// stored canonical response on duplicate keys instead of re-running.
package middleware

import (
	"bytes"
	"context"
	"net/http"
)

// IdempotencyStore is the contract the middleware needs. Implementations live
// in the storage package; the interface is defined here so middleware doesn't
// pull a storage dependency in.
type IdempotencyStore interface {
	Get(ctx context.Context, key string) (status int, body []byte, found bool, err error)
	Put(ctx context.Context, key string, status int, body []byte) error
}

// Idempotency wraps next so that POSTs carrying an Idempotency-Key are
// deduplicated against the store. A missing header is currently passed
// through; the "require header" 400 case lands in its own cycle.
func Idempotency(store IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				http.Error(w, "Idempotency-Key header is required", http.StatusBadRequest)
				return
			}

			if _, body, found, err := store.Get(r.Context(), key); err == nil && found {
				// PRD § API contracts: replay returns 200 even when the
				// original was 201 — "nothing was newly created this time."
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
				return
			}

			rec := newRecorder()
			next.ServeHTTP(rec, r)
			if rec.statusCode == 0 {
				rec.statusCode = http.StatusOK
			}
			body := rec.buf.Bytes()

			// Persist before flushing so a successful replay always sees a
			// committed row; persistence failures are non-fatal — the client
			// still gets their response, just without dedup on next retry.
			if rec.statusCode >= 200 && rec.statusCode < 300 {
				_ = store.Put(r.Context(), key, rec.statusCode, body)
			}

			for k, vs := range rec.headers {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.statusCode)
			_, _ = w.Write(body)
		})
	}
}

type recorder struct {
	headers    http.Header
	statusCode int
	buf        *bytes.Buffer
}

func newRecorder() *recorder {
	return &recorder{headers: make(http.Header), buf: &bytes.Buffer{}}
}

func (r *recorder) Header() http.Header         { return r.headers }
func (r *recorder) WriteHeader(s int)           { r.statusCode = s }
func (r *recorder) Write(b []byte) (int, error) { return r.buf.Write(b) }
