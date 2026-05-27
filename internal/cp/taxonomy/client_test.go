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
