package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// HealthProbeStore is the read-side surface the health-probes endpoint
// needs: device existence (for 404) + the per-probe rows.
type HealthProbeStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	ListHealthProbes(ctx context.Context, deviceID string) ([]registry.DeviceHealthProbe, error)
}

// HealthProbeListHandler serves GET /devices/{id}/health-probes — the
// per-device fleet-health-probe snapshot (#19) under a {probes: [...]}
// envelope. OS-agnostic by construction (ADR-034): names/states/status
// are the agent's vocabulary, never an OS verb.
type HealthProbeListHandler struct {
	store HealthProbeStore
}

func NewHealthProbeList(store HealthProbeStore) *HealthProbeListHandler {
	return &HealthProbeListHandler{store: store}
}

type healthProbeItem struct {
	Name           string         `json:"name"`
	Status         string         `json:"status"`
	State          string         `json:"state"`
	Details        map[string]any `json:"details"`
	LastObservedAt string         `json:"last_observed_at"` // RFC3339
}

type healthProbeListResponse struct {
	Probes []healthProbeItem `json:"probes"`
}

func (h *HealthProbeListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if _, err := h.store.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			http.Error(w, "device not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	probes, err := h.store.ListHealthProbes(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]healthProbeItem, 0, len(probes))
	for _, p := range probes {
		details := p.Details
		if details == nil {
			details = map[string]any{}
		}
		items = append(items, healthProbeItem{
			Name:           p.Name,
			Status:         p.Status,
			State:          p.State,
			Details:        details,
			LastObservedAt: p.LastObservedAt.UTC().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthProbeListResponse{Probes: items})
}
