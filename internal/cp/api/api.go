// Package api wires the Control Plane HTTP router.
package api

import (
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/enrollment"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type Deps struct {
	Registry *registry.Registry
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /enrollments", enrollment.New(d.Registry))
	return mux
}
