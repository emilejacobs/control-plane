package tailscale_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/tailscale"
)

// fakeAPI stands in for api.tailscale.com's create-key endpoint, recording the
// request so tests can assert the device key is minted single-use + tagged.
type fakeAPI struct {
	srv *httptest.Server

	gotMethod string
	gotPath   string
	gotAuth   string
	gotBody   map[string]any
	status    int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{status: http.StatusOK}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotMethod = r.Method
		f.gotPath = r.URL.Path
		f.gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &f.gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		if f.status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "k123",
				"key":     "tskey-auth-abc123",
				"expires": "2026-06-16T12:00:00Z",
			})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestMintAuthKey(t *testing.T) {
	api := newFakeAPI(t)
	c := tailscale.NewClient("oauth-token-xyz", "uknomi.org",
		tailscale.WithBaseURL(api.srv.URL))

	key, err := c.MintAuthKey(context.Background(), tailscale.MintOptions{
		Tags:          []string{"tag:edge-device"},
		ExpirySeconds: 3600,
		Description:   "uknomi device dev_abc123",
	})
	if err != nil {
		t.Fatalf("MintAuthKey: %v", err)
	}

	// --- request: path, auth, single-use + tagged capabilities ---
	if api.gotMethod != http.MethodPost {
		t.Errorf("method: got %s", api.gotMethod)
	}
	if !strings.Contains(api.gotPath, "/api/v2/tailnet/uknomi.org/keys") {
		t.Errorf("path: got %s", api.gotPath)
	}
	if api.gotAuth != "Bearer oauth-token-xyz" {
		t.Errorf("auth header: got %q", api.gotAuth)
	}
	create := digCreate(t, api.gotBody)
	if reusable, _ := create["reusable"].(bool); reusable {
		t.Error("key must be single-use (reusable=false)")
	}
	if ephemeral, _ := create["ephemeral"].(bool); !ephemeral {
		t.Error("key must be ephemeral")
	}
	if preauth, _ := create["preauthorized"].(bool); !preauth {
		t.Error("key must be preauthorized so the device joins without admin approval")
	}
	tags, _ := create["tags"].([]any)
	if len(tags) != 1 || tags[0] != "tag:edge-device" {
		t.Errorf("tags: got %v", tags)
	}

	// --- result ---
	if key.ID != "k123" || key.Key != "tskey-auth-abc123" {
		t.Errorf("key: got %+v", key)
	}
	if key.ExpiresAt.IsZero() {
		t.Error("ExpiresAt not parsed")
	}
}

// API errors propagate (no key returned).
func TestMintAuthKeyAPIError(t *testing.T) {
	api := newFakeAPI(t)
	api.status = http.StatusForbidden
	c := tailscale.NewClient("bad-token", "uknomi.org", tailscale.WithBaseURL(api.srv.URL))

	_, err := c.MintAuthKey(context.Background(), tailscale.MintOptions{Tags: []string{"tag:edge-device"}})
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
}

func digCreate(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	caps, ok := body["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("no capabilities in body: %v", body)
	}
	devices, ok := caps["devices"].(map[string]any)
	if !ok {
		t.Fatalf("no devices in capabilities: %v", caps)
	}
	create, ok := devices["create"].(map[string]any)
	if !ok {
		t.Fatalf("no create in devices: %v", devices)
	}
	return create
}
