// Package notify delivers fleet-notification digests to the configured
// channels — email via Amazon SES and MS Teams via a Workflows incoming
// webhook (#98). The FanOut implements ingest.Notifier; the reconciler hands it
// a digest + the per-send NotifyConfig (recipients + webhook URL) each tick, so
// the senders hold no config state.
package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
)

// EmailSender sends a rendered email to the recipient list. SESEmailSender is
// the production implementation.
type EmailSender interface {
	Send(ctx context.Context, recipients []string, subject, body string) error
}

// WebhookPoster POSTs a JSON payload to a webhook URL. HTTPWebhookPoster is the
// production implementation.
type WebhookPoster interface {
	Post(ctx context.Context, url string, payload []byte) error
}

// FanOut dispatches a digest to email + Teams per the NotifyConfig. An empty
// channel (no recipients / blank webhook) is skipped. A failure in one channel
// does not suppress the other; the returned error names every channel that
// failed, so the reconciler leaves the tick's alerts un-notified and retries.
type FanOut struct {
	email   EmailSender
	webhook WebhookPoster
}

func NewFanOut(email EmailSender, webhook WebhookPoster) *FanOut {
	return &FanOut{email: email, webhook: webhook}
}

func (f *FanOut) Notify(ctx context.Context, d ingest.Digest, cfg ingest.NotifyConfig) error {
	var errs []error

	if len(cfg.Recipients) > 0 && f.email != nil {
		subject, body := renderEmail(d)
		if err := f.email.Send(ctx, cfg.Recipients, subject, body); err != nil {
			errs = append(errs, fmt.Errorf("email: %w", err))
		}
	}

	if cfg.TeamsWebhookURL != "" && f.webhook != nil {
		if err := f.webhook.Post(ctx, cfg.TeamsWebhookURL, renderTeams(d)); err != nil {
			errs = append(errs, fmt.Errorf("teams: %w", err))
		}
	}

	return errors.Join(errs...)
}
