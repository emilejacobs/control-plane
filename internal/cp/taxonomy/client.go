package taxonomy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Brand is the upstream `/brand` element. CP captures Brand as flat
// metadata on Site (ADR-033 § 4) — no local table. The upstream
// returns numeric IDs and exposes no `active` field; a brand returned
// by `/brand` IS the active set, so the syncer walks every brand.
//
// Wire shape verified against api.uknomi.com 2026-05-27.
type Brand struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// UpstreamStore is one element of the `/brand/{id}/store` response.
// "Store" is the upstream's vocabulary; CP calls the same entity a
// Site. The upstream response is flat — `client_id` is a foreign key
// with no nested client metadata, and there is no `/client` endpoint
// to enrich from. The Runner derives a client name from the joined
// set of brands the client operates ("Burger King, Dunkin Donuts" for
// a client running both, "Eegee's" for a single-brand client) — the
// brand-name substitute for real client identity until the upstream
// API exposes it (#18 follow-up). There is no `active` field on the
// store; absence-from-walk is the only soft-delete signal.
//
// Wire shape verified against api.uknomi.com 2026-05-27. The upstream
// payload carries many other fields (address, geo, POS account, etc.)
// the mirror does not need; the JSON decoder ignores them.
type UpstreamStore struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	ClientID int    `json:"client_id"`
	BrandID  int    `json:"brand_id"`
}

// Client talks to the upstream HTTP API at api.uknomi.com (ADR-033 § 7).
// Auth is a single layer: POST /user/signin proxies through to Cognito
// and returns the raw InitiateAuth response; the client extracts the
// IdToken (the AccessToken is rejected by the API Gateway authorizer)
// and sends it as Bearer on subsequent calls. On HTTP 401 the client
// re-signs once transparently and retries — the runner stays free of
// token-lifecycle bookkeeping.
//
// Not safe for concurrent use; one Client per sync run.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	token    string
}

// NewClient binds an upstream base URL (e.g. "https://api.uknomi.com")
// and the Cognito service-account credentials the sync run will use.
// The internal http.Client has a 30s timeout — well above the upstream's
// observed response times, below CloudWatch's 30-minute Fargate clock.
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// signinResponse mirrors the raw Cognito InitiateAuth payload the
// upstream Lambda passes through unmodified. Only AuthenticationResult
// .IdToken is consumed — the AccessToken doesn't satisfy the API
// Gateway COGNITO_USER_POOLS authorizer on the protected routes.
type signinResponse struct {
	AuthenticationResult struct {
		IdToken string `json:"IdToken"`
	} `json:"AuthenticationResult"`
}

// SignIn exchanges the bound credentials for a Cognito IdToken. The
// returned token is also stashed on the Client so subsequent
// GetBrands/GetStores calls authenticate transparently.
func (c *Client) SignIn(ctx context.Context) (string, error) {
	body, err := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
	})
	if err != nil {
		return "", fmt.Errorf("marshal signin body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/user/signin", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build signin request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("signin: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("signin: HTTP %d: %s", resp.StatusCode, snippet)
	}
	var out signinResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode signin response: %w", err)
	}
	if out.AuthenticationResult.IdToken == "" {
		return "", fmt.Errorf("signin: empty IdToken in response")
	}
	c.token = out.AuthenticationResult.IdToken
	return c.token, nil
}

// GetBrands fetches the upstream `/brand` list. On HTTP 401 it
// re-signs once and retries, per ADR-033 § 7.
func (c *Client) GetBrands(ctx context.Context) ([]Brand, error) {
	var out []Brand
	if err := c.authedGet(ctx, "/brand", &out); err != nil {
		return nil, fmt.Errorf("get brands: %w", err)
	}
	return out, nil
}

// GetStores fetches the upstream `/brand/{id}/store` list for a brand.
// Same re-sign-on-401 behavior as GetBrands.
func (c *Client) GetStores(ctx context.Context, brandID int) ([]UpstreamStore, error) {
	var out []UpstreamStore
	if err := c.authedGet(ctx, fmt.Sprintf("/brand/%d/store", brandID), &out); err != nil {
		return nil, fmt.Errorf("get stores for brand %d: %w", brandID, err)
	}
	return out, nil
}

// authedGet performs an authenticated GET, decoding the JSON body
// into v on 2xx. On 401 it re-signs once via SignIn and retries; a
// second 401 surfaces as an error so the run-failure alarm fires.
func (c *Client) authedGet(ctx context.Context, path string, v any) error {
	resp, err := c.doGet(ctx, path)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if _, err := c.SignIn(ctx); err != nil {
			return fmt.Errorf("re-sign after 401: %w", err)
		}
		resp, err = c.doGet(ctx, path)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) doGet(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", path, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	return resp, nil
}
