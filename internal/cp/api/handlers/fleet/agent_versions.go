package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// VersionCatalog is the read surface the rollout target picker needs: the set
// of published agent versions the release catalog carries. The mutating
// counterpart (POST /agent-rollouts) validates a chosen version against the
// same catalog (agentrollout.ErrVersionNotFound).
type VersionCatalog interface {
	ListVersions(ctx context.Context) ([]string, error)
}

// AgentVersionsHandler serves GET /fleet/agent-versions — the published-version
// list that backs the rollout target picker (#42). Auth + site-scoped but not
// staff-gated, mirroring GET /sites: operators see the catalog to populate the
// picker; the actual rollout (POST /agent-rollouts) stays staff-only.
type AgentVersionsHandler struct {
	catalog VersionCatalog
}

func NewAgentVersions(catalog VersionCatalog) *AgentVersionsHandler {
	return &AgentVersionsHandler{catalog: catalog}
}

type versionsResponse struct {
	Versions []string `json:"versions"`
}

func (h *AgentVersionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	versions, err := h.catalog.ListVersions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sortVersionsDesc(versions)

	// Force a JSON array (not null) for an empty catalog so the picker's map
	// over the list is always safe.
	if versions == nil {
		versions = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versionsResponse{Versions: versions})
}

// sortVersionsDesc orders versions newest-first so the picker defaults to the
// latest release. Comparison is numeric-aware per dotted component (1.10.0 is
// newer than 1.2.0), falling back to lexical order for any non-numeric
// component (e.g. a "-rc1" suffix or an unexpected tag).
func sortVersionsDesc(versions []string) {
	sort.SliceStable(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
}

// compareVersions returns >0 if a is newer than b, <0 if older, 0 if equal.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for k := 0; k < n; k++ {
		// A missing trailing component counts as 0, so "1.4" == "1.4.0".
		ai, aok := 0, true
		bi, bok := 0, true
		if k < len(as) {
			ai, aok = atoiOK(as[k])
		}
		if k < len(bs) {
			bi, bok = atoiOK(bs[k])
		}
		if !aok || !bok {
			// Non-numeric component: fall back to a stable lexical compare of
			// the whole strings rather than guessing precedence.
			return strings.Compare(a, b)
		}
		if ai != bi {
			return ai - bi
		}
	}
	return 0
}

func atoiOK(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
