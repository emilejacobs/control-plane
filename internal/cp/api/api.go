// Package api wires the Control Plane HTTP router.
//
// State-mutating routes (POST/PUT/PATCH/DELETE) are registered through
// Builder.Post / .Put / .Patch / .Delete, which automatically wrap the
// handler in the idempotency middleware. Read-side routes go through
// Builder.Get. Tests use NewBuilderWith + Builder.MutatingRoutes to
// enforce that every mutator is wrapped (the ADR-012 CI gate).
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/auth"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/enrollment"
	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type Deps struct {
	Registry         *registry.Registry
	AuthN            *authn.AuthN
	AuthZ            *authz.AuthZ
	IdempotencyStore middleware.IdempotencyStore

	// CmdPublisher is the IoT-Core data-plane publisher used by the
	// Phase 2 slice 2 service-config PUT to push a config.update down
	// the cmd channel. nil disables the route (the rest of the API
	// continues to serve; tests that don't need the surface omit it).
	CmdPublisher devices.CmdPublisher

	// Audit is the sink every state-mutating handler writes audit entries
	// through. nil falls back to a discard Writer so tests that do not
	// care about audit assertions can omit it.
	Audit audit.Writer

	// Logger is the base slog.Logger that cplog.Middleware wraps per
	// request. nil falls back to slog.Default(); tests pass a discard
	// logger to keep -v output clean.
	Logger *slog.Logger

	// CORSAllowedOrigins is the exact-match allow list for the CORS
	// middleware. Empty disables CORS. Production passes the dashboard
	// origin (https://control.uknomi.com) from main; tests pass nothing
	// unless they specifically need to exercise CORS.
	CORSAllowedOrigins []string
}

// Route names a method + path pair for the CI-gate test to probe.
type Route struct {
	Method string
	Path   string
}

// Builder constructs the CP router. State-mutating registrations
// auto-wrap the handler in the idempotency middleware + the audit
// HTTP middleware (so a handler that forgets to call audit.Write still
// records an envelope row) and record the route so tests can enumerate them.
type Builder struct {
	mux      *http.ServeMux
	idem     func(http.Handler) http.Handler
	auditMW  func(http.Handler) http.Handler
	mutating []Route
}

func newBuilder(idem, auditMW func(http.Handler) http.Handler) *Builder {
	return &Builder{mux: http.NewServeMux(), idem: idem, auditMW: auditMW}
}

// Get registers a read-side route. No idempotency or audit wrapping.
func (b *Builder) Get(path string, h http.Handler) {
	b.mux.Handle("GET "+path, h)
}

// Post registers a state-mutating route. The handler is wrapped in
// audit (innermost — sees the handler's status) then idempotency
// (outermost — short-circuits before the handler runs). The route is
// recorded for the CI-gate test.
func (b *Builder) Post(path string, h http.Handler) {
	b.mux.Handle("POST "+path, b.idem(b.auditMW(h)))
	b.mutating = append(b.mutating, Route{Method: http.MethodPost, Path: path})
}

// Put registers a state-mutating PUT route. Same middleware stack as
// Post (audit innermost, idempotency outermost); recorded for the CI-
// gate test on the same footing.
func (b *Builder) Put(path string, h http.Handler) {
	b.mux.Handle("PUT "+path, b.idem(b.auditMW(h)))
	b.mutating = append(b.mutating, Route{Method: http.MethodPut, Path: path})
}

// Handler returns the underlying mux for serving.
func (b *Builder) Handler() http.Handler { return b.mux }

// MutatingRoutes returns the recorded state-mutating routes. Tests use this
// to verify each one rejects requests without Idempotency-Key.
func (b *Builder) MutatingRoutes() []Route { return b.mutating }

// enrollmentRateLimit caps a single source IP at 20 enrollment requests per
// hour (ADR-017) — bursty real waves stay well under it; a leaked bootstrap
// key cannot enroll an unbounded number of fake devices.
const (
	enrollmentRateLimit  = 20
	enrollmentRateWindow = time.Hour
)

// NewBuilderWith returns a fully-configured Builder. Tests use this to
// inspect the route table; production code uses NewRouter.
func NewBuilderWith(d Deps) *Builder {
	auditW := d.Audit
	if auditW == nil {
		auditW = audit.SlogOnly{}
	}
	b := newBuilder(middleware.Idempotency(d.IdempotencyStore), audit.HTTPMiddleware(auditW))
	// /healthz is the ALB target group health check (ADR-022). 200, empty body,
	// no auth. Tightening from 200-499 to 200 depends on this being live.
	b.Get("/healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	enrollLimiter := middleware.NewRateLimiter(enrollmentRateLimit, enrollmentRateWindow)
	b.Post("/enrollments", enrollLimiter.Middleware(enrollment.New(d.Registry, auditW)))
	if d.AuthN != nil {
		b.Get("/auth/first-run", auth.NewFirstRunStatus(d.AuthN))
		b.Post("/auth/first-run", auth.NewFirstRun(d.AuthN, auditW))
		b.Post("/auth/login", auth.NewLogin(d.AuthN, auditW))
		b.Post("/auth/refresh", auth.NewRefresh(d.AuthN, auditW))
		b.Post("/auth/logout", auth.NewLogout(d.AuthN, auditW))
		// Authenticated routes require a valid operator bearer token.
		// Every authenticated route except enrollment itself also sits
		// behind the forced-TOTP-enrollment gate; device reads additionally
		// run through the site-scope middleware.
		requireAuth := middleware.Auth(d.AuthN)
		requireEnrolled := middleware.RequireTotpEnrolled(d.AuthN)
		requireScope := middleware.Scope(d.AuthZ)
		b.Post("/auth/totp/enroll", requireAuth(auth.NewTotpEnroll(d.AuthN, auditW)))
		b.Get("/devices", requireAuth(requireEnrolled(requireScope(devices.NewList(d.Registry)))))
		b.Get("/devices/{id}", requireAuth(requireEnrolled(requireScope(devices.NewGet(d.Registry)))))
		// Phase 2 slice 2: PUT /devices/{id}/service-config. Requires
		// auth + TOTP + site scope (same gates as the read surface).
		// Skipped silently when CmdPublisher is nil — keeps tests that
		// don't exercise the downward channel running unchanged.
		if d.CmdPublisher != nil {
			b.Put("/devices/{id}/service-config",
				requireAuth(requireEnrolled(requireScope(devices.NewServiceConfigPut(d.Registry, d.CmdPublisher)))))
		}
	}
	return b
}

// NewRouter returns a ready-to-serve http.Handler with the cplog
// correlation+access-log middleware wrapped around every route, and the
// CORS middleware outside cplog so preflight responses are still logged
// (useful for diagnosing browser-side failures).
func NewRouter(d Deps) http.Handler {
	return middleware.Cors(d.CORSAllowedOrigins)(cplog.Middleware(d.Logger)(NewBuilderWith(d).Handler()))
}
