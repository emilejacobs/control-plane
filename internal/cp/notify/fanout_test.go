package notify_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/notify"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type fakeEmail struct {
	mu         sync.Mutex
	calls      int
	recipients []string
	subject    string
	body       string
	err        error
}

func (f *fakeEmail) Send(_ context.Context, recipients []string, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.recipients = recipients
	f.subject = subject
	f.body = body
	return f.err
}

type fakeWebhook struct {
	mu      sync.Mutex
	calls   int
	url     string
	payload []byte
	err     error
}

func (f *fakeWebhook) Post(_ context.Context, url string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.url = url
	f.payload = payload
	return f.err
}

func digestWith(opened, resolved int) ingest.Digest {
	d := ingest.Digest{}
	for i := 0; i < opened; i++ {
		d.Opened = append(d.Opened, ingest.AlertEvent{Kind: registry.UnhealthyOffline, DeviceID: "dev", Hostname: "mac-x"})
	}
	for i := 0; i < resolved; i++ {
		d.Resolved = append(d.Resolved, ingest.AlertEvent{Kind: registry.UnhealthyOffline, DeviceID: "dev", Hostname: "mac-x"})
	}
	return d
}

// With both channels configured, the fan-out sends an email to the recipients
// and posts to the Teams webhook, each once.
func TestFanOutDispatchesBothChannels(t *testing.T) {
	email := &fakeEmail{}
	webhook := &fakeWebhook{}
	f := notify.NewFanOut(email, webhook, "https://cp.test")

	err := f.Notify(context.Background(), digestWith(1, 0), ingest.NotifyConfig{
		Recipients:      []string{"ops@example.com"},
		TeamsWebhookURL: "https://hook.example/x",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if email.calls != 1 {
		t.Errorf("email calls = %d, want 1", email.calls)
	}
	if len(email.recipients) != 1 || email.recipients[0] != "ops@example.com" {
		t.Errorf("recipients = %v", email.recipients)
	}
	if email.subject == "" || email.body == "" {
		t.Errorf("email subject/body should be rendered, got %q / %q", email.subject, email.body)
	}
	if webhook.calls != 1 {
		t.Errorf("webhook calls = %d, want 1", webhook.calls)
	}
	if webhook.url != "https://hook.example/x" || len(webhook.payload) == 0 {
		t.Errorf("webhook url/payload = %q / %q", webhook.url, webhook.payload)
	}
}

// An empty channel config is skipped — no email when there are no recipients,
// no Teams post when the webhook URL is blank.
func TestFanOutSkipsEmptyChannels(t *testing.T) {
	email := &fakeEmail{}
	webhook := &fakeWebhook{}
	f := notify.NewFanOut(email, webhook, "https://cp.test")

	// Only email configured.
	if err := f.Notify(context.Background(), digestWith(1, 0), ingest.NotifyConfig{Recipients: []string{"a@b.c"}}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if email.calls != 1 || webhook.calls != 0 {
		t.Errorf("email=%d webhook=%d, want 1/0", email.calls, webhook.calls)
	}

	// Only webhook configured.
	if err := f.Notify(context.Background(), digestWith(1, 0), ingest.NotifyConfig{TeamsWebhookURL: "https://h/x"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if email.calls != 1 || webhook.calls != 1 {
		t.Errorf("email=%d webhook=%d, want 1/1", email.calls, webhook.calls)
	}
}

// If one channel fails, the fan-out still attempts the other and returns an
// error mentioning the failed channel (so the reconciler retries the tick).
func TestFanOutPartialFailureStillSendsOther(t *testing.T) {
	email := &fakeEmail{err: errors.New("ses down")}
	webhook := &fakeWebhook{}
	f := notify.NewFanOut(email, webhook, "https://cp.test")

	err := f.Notify(context.Background(), digestWith(1, 0), ingest.NotifyConfig{
		Recipients:      []string{"a@b.c"},
		TeamsWebhookURL: "https://h/x",
	})
	if err == nil {
		t.Fatal("expected an error when a channel fails")
	}
	if !strings.Contains(err.Error(), "email") {
		t.Errorf("error should name the failed channel, got %v", err)
	}
	if webhook.calls != 1 {
		t.Errorf("other channel still sent: webhook calls = %d, want 1", webhook.calls)
	}
}
