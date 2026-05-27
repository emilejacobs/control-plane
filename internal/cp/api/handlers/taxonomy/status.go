// Package taxonomy serves /taxonomy/* — the Settings-page surface
// for the clients/sites mirror (ADR-033 § 8). Read endpoint reports
// counts + last sync; write endpoint triggers an on-demand
// ecs:RunTask for the cmd/taxonomy-sync task def.
package taxonomy

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	mirror "github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

// StatusReader is the subset of *taxonomy.Store the handler needs.
type StatusReader interface {
	Status(ctx context.Context) (mirror.StatusSnapshot, error)
}

// StatusHandler serves GET /taxonomy/status — the staff-only "last
// successful sync" card on the Settings page.
type StatusHandler struct {
	reader StatusReader
}

// NewStatus binds a Store-like reader.
func NewStatus(r StatusReader) *StatusHandler { return &StatusHandler{reader: r} }

// statusResponse is the on-wire shape. last_synced_at is a pointer so
// "never synced" round-trips as JSON null rather than a 1970 epoch.
type statusResponse struct {
	ClientsTotal  int        `json:"clients_total"`
	ClientsActive int        `json:"clients_active"`
	SitesTotal    int        `json:"sites_total"`
	SitesActive   int        `json:"sites_active"`
	LastSyncedAt  *time.Time `json:"last_synced_at"`
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snap, err := h.reader.Status(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(statusResponse{
		ClientsTotal:  snap.ClientsTotal,
		ClientsActive: snap.ClientsActive,
		SitesTotal:    snap.SitesTotal,
		SitesActive:   snap.SitesActive,
		LastSyncedAt:  snap.LastSyncedAt,
	})
}
