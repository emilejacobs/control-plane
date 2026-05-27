package taxonomy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

// signinPayload is the Cognito-native shape api.uknomi.com's
// /user/signin returns (the upstream Lambda proxies InitiateAuth
// straight through). Tests build it via helper so the fixture stays
// faithful to the wire shape verified 2026-05-27.
func signinPayload(idToken string) string {
	b, _ := json.Marshal(map[string]any{
		"AuthenticationResult": map[string]any{
			"IdToken":      idToken,
			"AccessToken":  "fake-access",
			"RefreshToken": "fake-refresh",
			"ExpiresIn":    86400,
			"TokenType":    "Bearer",
		},
	})
	return string(b)
}

// TestClientSignInExtractsIdTokenFromAuthenticationResult verifies
// the upstream `/user/signin` wire shape: a Cognito-native
// {AuthenticationResult: {IdToken, AccessToken, ...}} envelope. The
// client extracts IdToken specifically — AccessToken is rejected by
// the API Gateway COGNITO_USER_POOLS authorizer on the protected
// routes (verified 2026-05-27).
func TestClientSignInExtractsIdTokenFromAuthenticationResult(t *testing.T) {
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
		_, _ = w.Write([]byte(signinPayload("the-real-id-jwt")))
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
	if token != "the-real-id-jwt" {
		t.Errorf("token: got %q want the-real-id-jwt (IdToken from AuthenticationResult)", token)
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

// TestClientSignInRejectsEmptyIdToken locks the contract: a 200 with
// AuthenticationResult missing IdToken (e.g. challenge response, or
// the API team changing the shape) must surface as an error rather
// than silently proceeding with an empty Bearer token.
func TestClientSignInRejectsEmptyIdToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"AuthenticationResult":{"AccessToken":"acc"}}`))
	}))
	defer srv.Close()

	c := taxonomy.NewClient(srv.URL, "u", "p")
	if _, err := c.SignIn(context.Background()); err == nil {
		t.Errorf("SignIn: want error on empty IdToken")
	}
}

// TestClientGetBrandsSendsBearer verifies the authenticated read path:
// GET /brand sends the SignIn-issued IdToken as Bearer and parses the
// real wire shape — a flat array of numeric-id brands (verified
// against api.uknomi.com 2026-05-27, no `active` field).
func TestClientGetBrandsSendsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/signin":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(signinPayload("jwt-1")))
		case "/brand":
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":12,"name":"Burger King"},
				{"id":13,"name":"Dunkin Donuts"}
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
	if brands[0].ID != 12 || brands[0].Name != "Burger King" {
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
			token := "jwt-2"
			if signins == 1 {
				token = "jwt-1"
			}
			_, _ = w.Write([]byte(signinPayload(token)))
		case "/brand":
			brandHits++
			// First /brand call: pretend the token expired. Second call:
			// honour the re-signed token.
			if r.Header.Get("Authorization") == "Bearer jwt-1" {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":12,"name":"BK"}]`))
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

// TestClientGetStoresReturnsFlatClientID verifies the per-brand store
// walk: GET /brand/{id}/store returns a flat array where each store
// carries `client_id` and `brand_id` as numeric foreign keys (no
// nested client object, no `active` field — verified against
// api.uknomi.com 2026-05-27). Extra upstream fields beyond what the
// mirror needs (address, geo, POS account, etc.) are silently ignored
// by the JSON decoder.
func TestClientGetStoresReturnsFlatClientID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/signin":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(signinPayload("jwt")))
		case r.URL.Path == "/brand/13/store":
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":50,"name":"DD09","client_id":14,"brand_id":13,"address_line_1":"114 Bruckner Blvd","country":"US"},
				{"id":51,"name":"DD10","client_id":14,"brand_id":13}
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
	stores, err := c.GetStores(context.Background(), 13)
	if err != nil {
		t.Fatalf("GetStores: %v", err)
	}
	if gotPath != "/brand/13/store" {
		t.Errorf("path: got %q want /brand/13/store", gotPath)
	}
	if len(stores) != 2 {
		t.Fatalf("stores: got %d", len(stores))
	}
	if stores[0].ID != 50 || stores[0].Name != "DD09" || stores[0].ClientID != 14 || stores[0].BrandID != 13 {
		t.Errorf("stores[0]: %+v", stores[0])
	}
}
