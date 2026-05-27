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

// TestTaxonomySyncEndpointStaffTriggersRunTask locks ADR-033 § 8: a
// staff POST /taxonomy/sync invokes ECS RunTask on the
// cp-taxonomy-sync task def and returns 202 with the new task ARN.
// The handler itself does no work beyond the RunTask call —
// concurrency is gated by the task's own pg_try_advisory_lock.
func TestTaxonomySyncEndpointStaffTriggersRunTask(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	fakeARN := "arn:aws:ecs:us-east-1:523612763411:task/uknomi/cafef00d"
	invoker := &recordingRunTaskInvoker{arn: fakeARN}
	srv := newTestServerWithRunTask(t, ctx, invoker)

	token := mintAccessToken(t, ctx, srv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/taxonomy/sync", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "tax-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d want 202", resp.StatusCode)
	}

	var body struct {
		TaskARN string `json:"task_arn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TaskARN != fakeARN {
		t.Errorf("task_arn: got %q want %q", body.TaskARN, fakeARN)
	}
	if invoker.calls != 1 {
		t.Errorf("RunTask calls: got %d want 1", invoker.calls)
	}
}

// TestTaxonomySyncEndpointForbidsNonStaff locks the staff-gate on
// the manual button: a non-staff operator gets 403 and the RunTask
// invoker is never called.
func TestTaxonomySyncEndpointForbidsNonStaff(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	invoker := &recordingRunTaskInvoker{arn: "arn:should:never:fire"}
	srv := newTestServerWithRunTask(t, ctx, invoker)

	token := mintNonStaffAccessToken(t, ctx, srv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/taxonomy/sync", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "tax-non-staff")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: got %d want 403", resp.StatusCode)
	}
	if invoker.calls != 0 {
		t.Errorf("RunTask must not be invoked for non-staff: calls=%d", invoker.calls)
	}
}

// recordingRunTaskInvoker is the test double for the AWS ECS RunTask
// call. Counts invocations and hands back a fixed ARN.
type recordingRunTaskInvoker struct {
	arn   string
	calls int
}

func (r *recordingRunTaskInvoker) Run(ctx context.Context) (string, error) {
	r.calls++
	return r.arn, nil
}

// TestSitesEndpointReturnsClientsWithSites locks the picker surface:
// GET /sites returns a nested tree of clients + their active sites so
// the device-deployment edit modal can render a single grouped list.
// Active-only by default; inactive sites are filtered.
func TestSitesEndpointReturnsClientsWithSites(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	store := taxonomy.NewStore(srv.Pool)
	synced := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	c14, _ := store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "14", Name: "Client #14", SyncedAt: synced})
	c11, _ := store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "11", Name: "Client #11", SyncedAt: synced})
	_, _ = store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "50", Name: "DD09", ClientID: c14, BrandName: "Dunkin Donuts", BrandExternalID: "13",
		Active: true, SyncedAt: synced,
	})
	_, _ = store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "60", Name: "BK Mesa", ClientID: c14, BrandName: "Burger King", BrandExternalID: "12",
		Active: true, SyncedAt: synced,
	})
	_, _ = store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "70", Name: "CCs Coffee", ClientID: c11, BrandName: "CCs Coffee", BrandExternalID: "9",
		Active: false, SyncedAt: synced, // shuttered upstream
	})

	token := mintAccessToken(t, ctx, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/sites", nil)
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
		Clients []struct {
			ID         string `json:"id"`
			ExternalID string `json:"external_id"`
			Name       string `json:"name"`
			Sites      []struct {
				ID              string `json:"id"`
				ExternalID      string `json:"external_id"`
				Name            string `json:"name"`
				BrandName       string `json:"brand_name"`
				BrandExternalID string `json:"brand_external_id"`
				Active          bool   `json:"active"`
			} `json:"sites"`
		} `json:"clients"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Client 11 has no active sites — it should be excluded from the picker.
	if len(body.Clients) != 1 {
		t.Fatalf("clients: got %d want 1 (client #11 has no active sites, must filter)", len(body.Clients))
	}
	c := body.Clients[0]
	if c.ExternalID != "14" || c.Name != "Client #14" {
		t.Errorf("client[0]: %+v", c)
	}
	if len(c.Sites) != 2 {
		t.Fatalf("client[0].sites: got %d want 2", len(c.Sites))
	}
	// Sites sorted by name for stable picker order: BK Mesa, DD09.
	if c.Sites[0].Name != "BK Mesa" || c.Sites[1].Name != "DD09" {
		t.Errorf("sites not name-ordered: %+v", c.Sites)
	}
	if c.Sites[0].BrandName != "Burger King" || c.Sites[0].BrandExternalID != "12" {
		t.Errorf("brand metadata on sites[0]: %+v", c.Sites[0])
	}
}

// TestSitesEndpointIncludeInactiveQueryParam covers the
// power-user/staff fallback: ?include_inactive=true returns inactive
// sites alongside actives so a staff operator can re-assign a device
// to a site that was swept (e.g. the upstream sync incorrectly
// flagged it).
func TestSitesEndpointIncludeInactiveQueryParam(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	store := taxonomy.NewStore(srv.Pool)
	synced := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	cid, _ := store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "1", Name: "Client #1", SyncedAt: synced})
	_, _ = store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "100", Name: "Active Site", ClientID: cid, BrandName: "X", BrandExternalID: "0",
		Active: true, SyncedAt: synced,
	})
	_, _ = store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "101", Name: "Shuttered Site", ClientID: cid, BrandName: "X", BrandExternalID: "0",
		Active: false, SyncedAt: synced,
	})

	token := mintAccessToken(t, ctx, srv)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/sites?include_inactive=true", nil)
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
		Clients []struct {
			Sites []struct {
				Name   string `json:"name"`
				Active bool   `json:"active"`
			} `json:"sites"`
		} `json:"clients"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Clients) != 1 || len(body.Clients[0].Sites) != 2 {
		t.Fatalf("include_inactive: want 1 client × 2 sites, got %+v", body)
	}
	gotInactive := false
	for _, s := range body.Clients[0].Sites {
		if !s.Active {
			gotInactive = true
		}
	}
	if !gotInactive {
		t.Error("?include_inactive=true: no inactive site present")
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
