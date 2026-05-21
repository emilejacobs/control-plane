// Package api wires the Control Plane HTTP router.
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

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	idem := middleware.Idempotency(d.IdempotencyStore)
	mux.Handle("POST /enrollments", idem(enrollment.New(d.Registry)))
	if d.DevDevicesGetEnabled {
		mux.Handle("GET /devices/{id}", devices.NewGet(d.Registry))
	}
	return mux
}
