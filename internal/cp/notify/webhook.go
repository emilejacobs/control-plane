package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
)

// HTTPWebhookPoster posts a JSON payload to a webhook URL — the MS Teams
// Workflows incoming webhook. A non-2xx response is an error so the reconciler
// leaves the tick's alerts un-notified and retries.
type HTTPWebhookPoster struct {
	client *http.Client
}

func NewHTTPWebhookPoster(client *http.Client) *HTTPWebhookPoster {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPWebhookPoster{client: client}
}

func (p *HTTPWebhookPoster) Post(ctx context.Context, url string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
