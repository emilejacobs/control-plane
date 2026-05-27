package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

// TestTaxonomyStatusEndpointStaffSeesCounts locks the cp-api read
// surface ADR-033 § 8 backs the Settings page with: a staff GET
// /taxonomy/status returns total/active counts for clients + sites
// plus the most recent last_synced_at. Numbers come straight from
// Store.Status so the handler is a thin pass-through.
func TestTaxonomyStatusEndpointStaffSeesCounts(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// Seed two clients + two sites via the storage layer.
	store := taxonomy.NewStore(srv.Pool)
	synced := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	c1, _ := store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "c1", Name: "A", SyncedAt: synced})
	_, _ = store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "c2", Name: "B", SyncedAt: synced})
	_, _ = store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "s1", Name: "S1", ClientID: c1, BrandName: "BK", BrandExternalID: "bk",
		Active: true, SyncedAt: synced,
	})
	_, _ = store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "s2", Name: "S2", ClientID: c1, BrandName: "BK", BrandExternalID: "bk",
		Active: false, SyncedAt: synced,
	})

	token := mintAccessToken(t, ctx, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/taxonomy/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}

	var body struct {
		ClientsTotal  int        `json:"clients_total"`
		ClientsActive int        `json:"clients_active"`
		SitesTotal    int        `json:"sites_total"`
		SitesActive   int        `json:"sites_active"`
		LastSyncedAt  *time.Time `json:"last_synced_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ClientsTotal != 2 || body.ClientsActive != 2 {
		t.Errorf("clients: %+v", body)
	}
	if body.SitesTotal != 2 || body.SitesActive != 1 {
		t.Errorf("sites: %+v", body)
	}
	if body.LastSyncedAt == nil || !body.LastSyncedAt.Equal(synced) {
		t.Errorf("LastSyncedAt: got %v want %v", body.LastSyncedAt, synced)
	}
}

// TestTaxonomyStatusEndpointForbidsNonStaff locks ADR-033 § 8: the
// taxonomy sync surface is admin-only — non-staff operators get 403,
// not the counts, so the Settings page section can be hidden client-
// side based on a 403 probe.
func TestTaxonomyStatusEndpointForbidsNonStaff(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	token := mintNonStaffAccessToken(t, ctx, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/taxonomy/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: got %d want 403", resp.StatusCode)
	}
}

// mintNonStaffAccessToken is the non-staff sibling of mintAccessToken.
// Inserts a TOTP-enrolled operator with is_staff=false and returns a
// signed access token reflecting that.
func mintNonStaffAccessToken(t *testing.T, ctx context.Context, srv *testServer) string {
	t.Helper()
	const operatorID = "00000000-0000-0000-0000-0000000000bb"
	const email = "site-reader@acmecorp.test"
	if _, err := srv.Pool.Exec(ctx, `
		INSERT INTO operators (id, email, password_hash, is_staff, totp_secret_encrypted)
		VALUES ($1, $2, 'unused-hash', false, $3)
		ON CONFLICT (id) DO NOTHING
	`, operatorID, email, []byte("totp-secret-ciphertext")); err != nil {
		t.Fatalf("insert non-staff operator: %v", err)
	}
	signer := authn.NewSigner(testSigningKey, time.Hour)
	token, err := signer.Issue(authn.TokenClaims{
		OperatorID: operatorID,
		Email:      email,
		IsStaff:    false,
	})
	if err != nil {
		t.Fatalf("mint non-staff access token: %v", err)
	}
	return token
}
