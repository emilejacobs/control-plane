package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/emilejacobs/control-plane/internal/cp/agentrollout"
	"github.com/emilejacobs/control-plane/internal/cp/api/middleware"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// RolloutStore is the persistence side of POST /agent-rollouts: resolve the
// target set against the visible fleet and stamp the desired version.
// *registry.Registry satisfies it.
type RolloutStore interface {
	List(ctx context.Context) ([]registry.Device, error)
	SetDesiredAgentVersion(ctx context.Context, deviceIDs []string, version string) (int, error)
}

// UpdatePusher fans the agent.update command out to a device set.
// *agentrollout.Pusher satisfies it.
type UpdatePusher interface {
	PushMany(ctx context.Context, deviceIDs []string, version, correlationID string) (int, error)
}

// AgentRolloutPostHandler serves POST /agent-rollouts — the staff surface
// that starts (or re-targets, or aborts) an agent rollout by setting the
// per-device desired version on a target set (issue #40, ADR-035 §1/§4).
// There is no campaign entity: this stamp + the audit entry it writes IS the
// rollout record; canary, promote and abort are all just another POST with a
// different set.
//
// The desired version is persisted first, then agent.update is pushed
// best-effort to the targeted devices that are online — offline or
// push-failed devices converge via the cp-ingest reconcile path
// (reconnect/heartbeat), so a partial push still returns 202 with the
// pushed count.
type AgentRolloutPostHandler struct {
	store    RolloutStore
	catalog  agentrollout.ManifestSource
	pusher   UpdatePusher
	audit    audit.Writer
	newCmdID func() string
}

func NewAgentRolloutPost(store RolloutStore, catalog agentrollout.ManifestSource, pusher UpdatePusher, auditW audit.Writer) *AgentRolloutPostHandler {
	return &AgentRolloutPostHandler{store: store, catalog: catalog, pusher: pusher, audit: auditW, newCmdID: newRandomID}
}

type agentRolloutRequest struct {
	Version   string   `json:"version"`
	DeviceIDs []string `json:"device_ids"`
	SiteID    string   `json:"site_id"`
	All       bool     `json:"all"`
}

func (h *AgentRolloutPostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req agentRolloutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRolloutError(w, http.StatusBadRequest, "agent_rollout.bad_payload", "body is not valid JSON")
		return
	}
	if req.Version == "" {
		writeRolloutError(w, http.StatusBadRequest, "agent_rollout.bad_payload", "version is required")
		return
	}
	selectors := 0
	if len(req.DeviceIDs) > 0 {
		selectors++
	}
	if req.SiteID != "" {
		selectors++
	}
	if req.All {
		selectors++
	}
	if selectors != 1 {
		writeRolloutError(w, http.StatusBadRequest, "agent_rollout.bad_payload",
			"exactly one of device_ids, site_id, all is required")
		return
	}

	// Catalog check before anything is stamped: a rollout must never target
	// a version the release catalog doesn't carry (it would sit in-flight
	// forever, re-pushing an unfetchable update on every reconnect).
	if _, err := h.catalog.Manifest(r.Context(), req.Version); err != nil {
		if errors.Is(err, agentrollout.ErrVersionNotFound) {
			writeRolloutError(w, http.StatusBadRequest, "agent_rollout.unknown_version",
				"no release manifest for version "+req.Version)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	fleet, err := h.store.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	targets, online := resolveTargets(fleet, req)
	if len(targets) == 0 {
		writeRolloutError(w, http.StatusNotFound, "agent_rollout.no_targets",
			"target set matched no devices")
		return
	}

	targeted, err := h.store.SetDesiredAgentVersion(r.Context(), targets, req.Version)
	if err != nil {
		h.writeAudit(r, req.Version, targets, 0, "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	correlationID := cplog.CorrelationIDFromContext(r.Context())
	if correlationID == "" {
		correlationID = h.newCmdID()
	}

	// Best-effort initial push to the online targets; offline (and any
	// push-failed) devices converge via reconcile. The catalog was checked
	// above, so a failure here is downstream (S3/IoT) — desired is already
	// persisted either way, which is the durable intent.
	pushed, pushErr := h.pusher.PushMany(r.Context(), online, req.Version, correlationID)
	if pushErr != nil {
		h.writeAudit(r, req.Version, targets, pushed, "error", pushErr)
		http.Error(w, "downstream push failed: "+pushErr.Error(), http.StatusBadGateway)
		return
	}

	h.writeAudit(r, req.Version, targets, pushed, "success", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		CorrelationID string `json:"correlation_id"`
		Targeted      int    `json:"targeted"`
		Pushed        int    `json:"pushed"`
	}{CorrelationID: correlationID, Targeted: targeted, Pushed: pushed})
}

// resolveTargets filters the visible fleet down to the request's target set,
// returning all target ids plus the online subset (the initial-push list).
func resolveTargets(fleet []registry.Device, req agentRolloutRequest) (targets, online []string) {
	wanted := func(d registry.Device) bool {
		switch {
		case req.All:
			return true
		case req.SiteID != "":
			return d.SiteID != nil && *d.SiteID == req.SiteID
		default:
			for _, id := range req.DeviceIDs {
				if d.ID == id {
					return true
				}
			}
			return false
		}
	}
	for _, d := range fleet {
		if !wanted(d) {
			continue
		}
		targets = append(targets, d.ID)
		if d.IsOnline {
			online = append(online, d.ID)
		}
	}
	return targets, online
}

func (h *AgentRolloutPostHandler) writeAudit(r *http.Request, version string, targets []string, pushed int, outcome string, opErr error) {
	claims, _ := middleware.OperatorFromContext(r.Context())
	payload := map[string]any{
		"version":    version,
		"device_ids": targets,
		"targeted":   len(targets),
		"pushed":     pushed,
	}
	if opErr != nil {
		payload["err"] = opErr.Error()
	}
	_ = h.audit.Write(r.Context(), audit.Entry{
		Action: "audit.agent_rollout_set", ActorID: claims.OperatorID, ActorType: audit.ActorOperator,
		ResourceKind: "agent_rollout", ResourceID: version, Outcome: outcome,
		SourceIP: clientIP(r), UserAgent: r.UserAgent(),
		Payload: payload,
	})
}

func writeRolloutError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}
