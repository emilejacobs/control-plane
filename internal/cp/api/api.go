// Package api wires the Control Plane HTTP router.
//
// State-mutating routes (POST/PUT/PATCH/DELETE) are registered through
// Builder.Post / .Put / .Patch / .Delete, which automatically wrap the
// handler in the idempotency middleware. Read-side routes go through
// Builder.Get. Tests use NewBuilderWith + Builder.MutatingRoutes to
// enforce that every mutator is wrapped (the ADR-012 CI gate).
package api

import (
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/enrollment"
	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type Deps struct {
	Registry         *registry.Registry
	IdempotencyStore middleware.IdempotencyStore

	// DevDevicesGetEnabled exposes GET /devices/{id} without auth. Issue 03's
	// dev-only escape hatch; #04 removes the flag and adds the auth middleware.
	DevDevicesGetEnabled bool
}

// Route names a method + path pair for the CI-gate test to probe.
type Route struct {
	Method string
	Path   string
}

// Builder constructs the CP router. State-mutating registrations
// auto-wrap the handler in the idempotency middleware and record the
// route so tests can enumerate them.
type Builder struct {
	mux      *http.ServeMux
	idem     func(http.Handler) http.Handler
	mutating []Route
}

func newBuilder(idem func(http.Handler) http.Handler) *Builder {
	return &Builder{mux: http.NewServeMux(), idem: idem}
}

// Get registers a read-side route. No idempotency wrapping.
func (b *Builder) Get(path string, h http.Handler) {
	b.mux.Handle("GET "+path, h)
}

// Post registers a state-mutating route. The handler is automatically
// wrapped in the idempotency middleware; the route is recorded for the
// CI-gate test.
func (b *Builder) Post(path string, h http.Handler) {
	b.mux.Handle("POST "+path, b.idem(h))
	b.mutating = append(b.mutating, Route{Method: http.MethodPost, Path: path})
}

// Handler returns the underlying mux for serving.
func (b *Builder) Handler() http.Handler { return b.mux }

// MutatingRoutes returns the recorded state-mutating routes. Tests use this
// to verify each one rejects requests without Idempotency-Key.
func (b *Builder) MutatingRoutes() []Route { return b.mutating }

// NewBuilderWith returns a fully-configured Builder. Tests use this to
// inspect the route table; production code uses NewRouter.
func NewBuilderWith(d Deps) *Builder {
	b := newBuilder(middleware.Idempotency(d.IdempotencyStore))
	b.Post("/enrollments", enrollment.New(d.Registry))
	if d.DevDevicesGetEnabled {
		b.Get("/devices/{id}", devices.NewGet(d.Registry))
	}
	return b
}

// NewRouter returns a ready-to-serve http.Handler.
func NewRouter(d Deps) http.Handler {
	return NewBuilderWith(d).Handler()
}
