package taxonomy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

// TestClientSignInPostsCredentialsAndReturnsToken is the cycle-6 tracer
// for the upstream HTTP client. POST /user/signin against api.uknomi.com
// (ADR-033 § 7) takes a JSON {username, password} body and returns a
// Cognito JWT the caller threads through Authorization: Bearer on
// subsequent requests. The client wraps that single exchange.
func TestClientSignInPostsCredentialsAndReturnsToken(t *testing.T) {
	var gotMethod, gotPath, gotContentType string
	var gotBody struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "fake-jwt"})
	}))
	defer srv.Close()

	c := taxonomy.NewClient(srv.URL, "user@uknomi", "hunter2")
	token, err := c.SignIn(context.Background())
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotPath != "/user/signin" {
		t.Errorf("path: got %q want /user/signin", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type: got %q want application/json", gotContentType)
	}
	if gotBody.Username != "user@uknomi" || gotBody.Password != "hunter2" {
		t.Errorf("body: got %+v", gotBody)
	}
	if token != "fake-jwt" {
		t.Errorf("token: got %q want fake-jwt", token)
	}
}

// TestClientSignInReturnsErrorOnNon200 covers credential failure: a
// 401 from the upstream is a hard error from the syncer's perspective
// (the run cannot proceed without a token) and must surface as a
// non-nil error so the alarm fires.
func TestClientSignInReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad creds", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := taxonomy.NewClient(srv.URL, "user", "wrong")
	if _, err := c.SignIn(context.Background()); err == nil {
		t.Errorf("SignIn: got nil error, want non-nil for 401")
	}
}

// TestClientGetBrandsSendsBearer verifies the authenticated read path:
// GET /brand requires a prior SignIn, threads the JWT through the
// Authorization header per ADR-033 § 7, and parses the brand list.
func TestClientGetBrandsSendsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/signin":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt-1"})
		case "/brand":
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":"b1","name":"Burger King","active":true},
				{"id":"b2","name":"Dunkin Donuts","active":true}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := taxonomy.NewClient(srv.URL, "u", "p")
	if _, err := c.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	brands, err := c.GetBrands(context.Background())
	if err != nil {
		t.Fatalf("GetBrands: %v", err)
	}
	if gotAuth != "Bearer jwt-1" {
		t.Errorf("Authorization: got %q want %q", gotAuth, "Bearer jwt-1")
	}
	if len(brands) != 2 {
		t.Fatalf("brands: got %d want 2", len(brands))
	}
	if brands[0].ID != "b1" || brands[0].Name != "Burger King" || !brands[0].Active {
		t.Errorf("brands[0]: %+v", brands[0])
	}
}

// TestClientGetBrandsReSignsOn401 locks the "re-sign on 401" contract:
// when the stashed JWT is rejected mid-run, the client transparently
// calls /user/signin again with the bound credentials and retries
// once. ADR-033 § 7 keeps the runner free of token-lifecycle bookkeeping.
func TestClientGetBrandsReSignsOn401(t *testing.T) {
	var signins, brandHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/signin":
			signins++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"jwt-` + map[bool]string{true: "1", false: "2"}[signins == 1] + `"}`))
		case "/brand":
			brandHits++
			// First /brand call: pretend the token expired. Second call:
			// honour the re-signed token.
			if r.Header.Get("Authorization") == "Bearer jwt-1" {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"b1","name":"BK","active":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := taxonomy.NewClient(srv.URL, "u", "p")
	if _, err := c.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetBrands(context.Background()); err != nil {
		t.Fatalf("GetBrands: %v", err)
	}
	if signins != 2 {
		t.Errorf("signins: got %d want 2 (initial + re-sign on 401)", signins)
	}
	if brandHits != 2 {
		t.Errorf("brand hits: got %d want 2 (initial + retry)", brandHits)
	}
}

// TestClientGetStoresReturnsClientNested verifies the per-brand store
// walk: GET /brand/{id}/store returns stores with their nested client
// info — the syncer dedupes clients across brands via the nested IDs.
func TestClientGetStoresReturnsClientNested(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/signin":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"jwt"}`))
		case r.URL.Path == "/brand/b1/store":
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":"s1","name":"Site 1","active":true,"client":{"id":"c1","name":"Rao"}},
				{"id":"s2","name":"Site 2","active":false,"client":{"id":"c1","name":"Rao"}}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := taxonomy.NewClient(srv.URL, "u", "p")
	if _, err := c.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	stores, err := c.GetStores(context.Background(), "b1")
	if err != nil {
		t.Fatalf("GetStores: %v", err)
	}
	if gotPath != "/brand/b1/store" {
		t.Errorf("path: got %q", gotPath)
	}
	if len(stores) != 2 {
		t.Fatalf("stores: got %d", len(stores))
	}
	if stores[0].Client.ID != "c1" || stores[0].Client.Name != "Rao" {
		t.Errorf("stores[0].Client: %+v", stores[0].Client)
	}
	if stores[1].Active != false {
		t.Errorf("stores[1].Active: got %v want false", stores[1].Active)
	}
}
