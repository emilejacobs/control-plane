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

// Client talks to the upstream HTTP API at api.uknomi.com (ADR-033 § 7).
// Auth is a single layer: POST /user/signin exchanges
// {username, password} for a Cognito JWT held in memory for the
// remainder of the sync run; subsequent GETs send it as Bearer.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
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

// SignIn exchanges the bound credentials for a Cognito JWT.
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
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode signin response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("signin: empty token in response")
	}
	return out.Token, nil
}
