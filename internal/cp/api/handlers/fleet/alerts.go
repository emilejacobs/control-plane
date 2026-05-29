// Package fleet serves fleet-wide roll-ups that span devices — as opposed to
// the per-device reads under handlers/devices. The first is GET /fleet/alerts
// (#21): the Overview alerts dashboard's source of unhealthy signals.
package fleet

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// AlertStore is the read surface the alerts endpoint needs: the site-scoped
// fleet roll-up. Scoping is applied inside the store from the request's
// resolved SiteFilter, so the handler stays oblivious to authz.
type AlertStore interface {
	FleetAlerts(ctx context.Context) (registry.FleetAlerts, error)
}

// AlertsHandler serves GET /fleet/alerts — the alert-only roll-up of devices
// red/yellow per probe and stopped per service, grouped by type, with the
// affected device ids inline so the UI can drill down without a second call.
type AlertsHandler struct {
	store AlertStore
}

func NewAlerts(store AlertStore) *AlertsHandler {
	return &AlertsHandler{store: store}
}

type probeAlert struct {
	ProbeName string   `json:"probe_name"`
	Red       []string `json:"red"`
	Yellow    []string `json:"yellow"`
}

type serviceAlert struct {
	ServiceName string   `json:"service_name"`
	Stopped     []string `json:"stopped"`
}

type alertsResponse struct {
	Probes   []probeAlert   `json:"probes"`
	Services []serviceAlert `json:"services"`
}

func (h *AlertsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	alerts, err := h.store.FleetAlerts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := alertsResponse{
		Probes:   make([]probeAlert, 0, len(alerts.Probes)),
		Services: make([]serviceAlert, 0, len(alerts.Services)),
	}
	for _, p := range alerts.Probes {
		resp.Probes = append(resp.Probes, probeAlert{
			ProbeName: p.ProbeName,
			Red:       nonNil(p.Red),
			Yellow:    nonNil(p.Yellow),
		})
	}
	for _, s := range alerts.Services {
		resp.Services = append(resp.Services, serviceAlert{
			ServiceName: s.ServiceName,
			Stopped:     nonNil(s.Stopped),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// nonNil returns an empty slice for nil so the JSON field serializes as []
// rather than null, letting the UI map over it unconditionally.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
