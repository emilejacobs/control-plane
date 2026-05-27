package taxonomy

import (
	"context"
	"encoding/json"
	"net/http"

	mirror "github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

// SitesLister is the subset of *taxonomy.Store the handler reads.
type SitesLister interface {
	ListClientsWithSites(ctx context.Context, includeInactive bool) ([]mirror.ClientWithSites, error)
}

// SitesHandler serves GET /sites — the picker tree used by the
// device-deployment edit modal. Returns clients grouped with their
// sites; active-only by default, with ?include_inactive=true for
// staff re-assigning a swept site.
type SitesHandler struct {
	reader SitesLister
}

func NewSites(r SitesLister) *SitesHandler { return &SitesHandler{reader: r} }

type sitesResponse struct {
	Clients []clientNode `json:"clients"`
}

type clientNode struct {
	ID         string     `json:"id"`
	ExternalID string     `json:"external_id"`
	Name       string     `json:"name"`
	Sites      []siteNode `json:"sites"`
}

type siteNode struct {
	ID              string `json:"id"`
	ExternalID      string `json:"external_id"`
	Name            string `json:"name"`
	BrandName       string `json:"brand_name"`
	BrandExternalID string `json:"brand_external_id"`
	Active          bool   `json:"active"`
}

func (h *SitesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	includeInactive := r.URL.Query().Get("include_inactive") == "true"

	tree, err := h.reader.ListClientsWithSites(r.Context(), includeInactive)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := sitesResponse{Clients: make([]clientNode, 0, len(tree))}
	for _, c := range tree {
		node := clientNode{
			ID:         c.ID,
			ExternalID: c.ExternalID,
			Name:       c.Name,
			Sites:      make([]siteNode, 0, len(c.Sites)),
		}
		for _, s := range c.Sites {
			node.Sites = append(node.Sites, siteNode{
				ID:              s.ID,
				ExternalID:      s.ExternalID,
				Name:            s.Name,
				BrandName:       s.BrandName,
				BrandExternalID: s.BrandExternalID,
				Active:          s.Active,
			})
		}
		out.Clients = append(out.Clients, node)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
