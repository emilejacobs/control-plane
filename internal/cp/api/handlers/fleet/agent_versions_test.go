package fleet_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/fleet"
)

// fakeVersionCatalog stubs the release catalog's version listing.
type fakeVersionCatalog struct {
	versions []string
	err      error
}

func (c *fakeVersionCatalog) ListVersions(context.Context) ([]string, error) {
	return c.versions, c.err
}

type versionsBody struct {
	Versions []string `json:"versions"`
}

// GET /fleet/agent-versions lists the published catalog versions for the
// rollout target picker (#42). The picker defaults to the newest, so the
// handler sorts newest-first with a numeric-aware (not lexical) comparison —
// 1.10.0 is newer than 1.2.0.
func TestAgentVersionsListsCatalogNewestFirst(t *testing.T) {
	cat := &fakeVersionCatalog{versions: []string{"1.2.0", "1.10.0", "1.4.1", "1.4.0"}}
	h := fleet.NewAgentVersions(cat)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fleet/agent-versions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body)
	}
	var got versionsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body: %s", err, rec.Body)
	}
	want := []string{"1.10.0", "1.4.1", "1.4.0", "1.2.0"}
	if len(got.Versions) != len(want) {
		t.Fatalf("versions = %v; want %v", got.Versions, want)
	}
	for i := range want {
		if got.Versions[i] != want[i] {
			t.Errorf("versions[%d] = %q; want %q (full: %v)", i, got.Versions[i], want[i], got.Versions)
		}
	}
}

// An empty catalog must serialize as [] (not null) so the picker's .map is safe.
func TestAgentVersionsEmptyIsArrayNotNull(t *testing.T) {
	h := fleet.NewAgentVersions(&fakeVersionCatalog{versions: nil})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fleet/agent-versions", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, `"versions":[]`) {
		t.Errorf("empty catalog should serialize versions as []; got %s", body)
	}
}

// A catalog read failure surfaces as 500 (the picker shows a load error).
func TestAgentVersionsCatalogErrorIs500(t *testing.T) {
	h := fleet.NewAgentVersions(&fakeVersionCatalog{err: errors.New("s3 unavailable")})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fleet/agent-versions", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rec.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
