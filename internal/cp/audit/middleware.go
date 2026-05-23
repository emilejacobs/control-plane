package audit

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
)

// HTTPMiddleware writes a generic audit envelope for any state-mutating
// request (POST, PUT, PATCH, DELETE) whose handler did not itself call
// audit.Write. Read-side methods are skipped entirely.
//
// The contract: install audit.WithTracker on the request ctx, run the
// inner handler, then check audit.handled(ctx). If false, write an
// envelope with method/path/status. Writer implementations call
// audit.markHandled(ctx) inside Write, so a handler that audited even
// once suppresses the middleware's envelope.
//
// The action of the envelope is "audit.http.<method>" lowercased — e.g.
// "audit.http.post" — so the table makes the missing-explicit-call case
// easy to spot.
func HTTPMiddleware(w Writer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if !mutating(r.Method) {
				next.ServeHTTP(rw, r)
				return
			}
			ctx, tracker := withTracker(r.Context())
			rec := &statusRecorder{ResponseWriter: rw, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))
			if tracker.handled() {
				return
			}
			_ = w.Write(ctx, Entry{
				Action:    "audit.http." + strings.ToLower(r.Method),
				ActorType: ActorSystem,
				Outcome:   outcomeForStatus(rec.status),
				SourceIP:  clientIP(r),
				UserAgent: r.UserAgent(),
				Payload: map[string]any{
					"method": r.Method,
					"path":   r.URL.Path,
					"status": rec.status,
				},
			})
		})
	}
}

func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func outcomeForStatus(s int) string {
	switch {
	case s >= 500:
		return "error"
	case s >= 400:
		return "failure"
	default:
		return "success"
	}
}

// tracker is a per-request flag toggled when a Writer's Write runs.
type tracker struct{ seen atomic.Bool }

func (t *tracker) markSeen() { t.seen.Store(true) }
func (t *tracker) handled() bool {
	return t.seen.Load()
}

type trackerKey struct{}

func withTracker(ctx context.Context) (context.Context, *tracker) {
	t := &tracker{}
	return context.WithValue(ctx, trackerKey{}, t), t
}

func trackerFrom(ctx context.Context) *tracker {
	t, _ := ctx.Value(trackerKey{}).(*tracker)
	return t
}

// markHandled flips the per-request tracker (if HTTPMiddleware installed
// one) so the middleware suppresses its envelope. Writer impls call this
// inside their Write methods; it is a no-op when no tracker is in ctx.
func markHandled(ctx context.Context) {
	if t := trackerFrom(ctx); t != nil {
		t.markSeen()
	}
}

// clientIP returns the source address without the port. Duplicates
// helpers in middleware/ratelimit and the auth handler; a shared httpx
// package can consolidate when there is a third caller worth pulling in.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

// statusRecorder mirrors cplog's recorder so the middleware can read the
// response status. Exporting cplog's would widen its API; duplicating one
// 10-line struct is the disciplined trade for now.
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
