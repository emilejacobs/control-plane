package fleet

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// RolloutDeviceLister is the read surface the rollout view needs. Site
// scoping is applied inside the store from the request's resolved
// SiteFilter (registry.List), so a scoped operator sees their slice of the
// rollout and staff see the fleet.
type RolloutDeviceLister interface {
	List(ctx context.Context) ([]registry.Device, error)
}

// AgentRolloutHandler serves GET /fleet/agent-rollout — the issue-#40
// desired-vs-reported view. Rollout state is derived per device (ADR-035 §4,
// no campaign entity): done = reported matches desired, in_flight = targeted
// but drifted, untargeted = no desired version set.
type AgentRolloutHandler struct {
	store RolloutDeviceLister
}

func NewAgentRollout(store RolloutDeviceLister) *AgentRolloutHandler {
	return &AgentRolloutHandler{store: store}
}

type rolloutCounts struct {
	Done       int `json:"done"`
	InFlight   int `json:"in_flight"`
	RolledBack int `json:"rolled_back"`
	Untargeted int `json:"untargeted"`
}

type rolloutDevice struct {
	ID              string  `json:"id"`
	Hostname        string  `json:"hostname"`
	SiteID          *string `json:"site_id"`
	SiteName        *string `json:"site_name"`
	ClientName      *string `json:"client_name"`
	ReportedVersion string  `json:"reported_version"`
	DesiredVersion  *string `json:"desired_version"`
	IsOnline        bool    `json:"is_online"`
	State           string  `json:"state"`
}

type rolloutResponse struct {
	Counts  rolloutCounts   `json:"counts"`
	Devices []rolloutDevice `json:"devices"`
}

func (h *AgentRolloutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	devices, err := h.store.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := rolloutResponse{Devices: make([]rolloutDevice, 0, len(devices))}
	for _, d := range devices {
		state := "untargeted"
		switch {
		case d.DesiredAgentVersion == nil:
			resp.Counts.Untargeted++
		case *d.DesiredAgentVersion == d.AgentVersion:
			state = "done"
			resp.Counts.Done++
		case d.RolledBackVersion != nil && *d.RolledBackVersion == *d.DesiredAgentVersion:
			// The device tried the desired version and the resident wrapper
			// reverted it — not merely in flight. The stale-rollback case
			// (a rollback recorded for a now-superseded desired) falls through
			// to in_flight because the versions no longer match.
			state = "rolled_back"
			resp.Counts.RolledBack++
		default:
			state = "in_flight"
			resp.Counts.InFlight++
		}
		resp.Devices = append(resp.Devices, rolloutDevice{
			ID:              d.ID,
			Hostname:        d.Hostname,
			SiteID:          d.SiteID,
			SiteName:        d.SiteName,
			ClientName:      d.ClientName,
			ReportedVersion: d.AgentVersion,
			DesiredVersion:  d.DesiredAgentVersion,
			IsOnline:        d.IsOnline,
			State:           state,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
