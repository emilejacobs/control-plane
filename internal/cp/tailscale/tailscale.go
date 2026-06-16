// Package tailscale mints per-device Tailscale auth keys for Commission
// (ADR-036 §4). Each key is ephemeral, single-use, preauthorized, and tagged —
// so a leaked key can enroll at most the one device it was minted for, removing
// the shared-fleet-key blast radius the old install carried.
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Key is a minted auth key. Key is secret — callers must never log it.
type Key struct {
	ID        string
	Key       string
	ExpiresAt time.Time
}

// MintOptions parameterise a single device key.
type MintOptions struct {
	Tags          []string // ACL tags, e.g. ["tag:edge-device"]
	ExpirySeconds int       // key validity window
	Description    string   // traceability, e.g. "uknomi device <id>"
}

// Minter mints ephemeral single-use auth keys. Commission depends on this
// interface; tests and offline runs use Fake.
type Minter interface {
	MintAuthKey(ctx context.Context, opts MintOptions) (Key, error)
}

const defaultBaseURL = "https://api.tailscale.com"

// Client is the real Tailscale API minter. The token is a Bearer credential
// loaded from Secrets Manager (ADR-036 §4) — held in memory only, never logged.
type Client struct {
	httpClient *http.Client
	baseURL    string
	tailnet    string
	token      string
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base (for tests).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// NewClient returns a minter for tailnet, authenticating with token.
func NewClient(token, tailnet string, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		tailnet:    tailnet,
		token:      token,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type createCaps struct {
	Reusable      bool     `json:"reusable"`
	Ephemeral     bool     `json:"ephemeral"`
	Preauthorized bool     `json:"preauthorized"`
	Tags          []string `json:"tags"`
}

type createKeyRequest struct {
	Capabilities struct {
		Devices struct {
			Create createCaps `json:"create"`
		} `json:"devices"`
	} `json:"capabilities"`
	ExpirySeconds int    `json:"expirySeconds,omitempty"`
	Description   string `json:"description,omitempty"`
}

type createKeyResponse struct {
	ID      string    `json:"id"`
	Key     string    `json:"key"`
	Expires time.Time `json:"expires"`
}

// MintAuthKey creates an ephemeral, single-use, preauthorized, tagged key.
func (c *Client) MintAuthKey(ctx context.Context, opts MintOptions) (Key, error) {
	var body createKeyRequest
	body.Capabilities.Devices.Create = createCaps{
		Reusable:      false, // single-use
		Ephemeral:     true,
		Preauthorized: true,
		Tags:          opts.Tags,
	}
	body.ExpirySeconds = opts.ExpirySeconds
	body.Description = opts.Description

	raw, err := json.Marshal(body)
	if err != nil {
		return Key{}, fmt.Errorf("marshal create-key request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tailnet/%s/keys", c.baseURL, c.tailnet)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return Key{}, fmt.Errorf("build create-key request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Key{}, fmt.Errorf("tailscale create key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Key{}, fmt.Errorf("tailscale create key: HTTP %d", resp.StatusCode)
	}

	var out createKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Key{}, fmt.Errorf("decode create-key response: %w", err)
	}
	return Key{ID: out.ID, Key: out.Key, ExpiresAt: out.Expires}, nil
}
