package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/emilejacobs/control-plane/internal/cp/notify"
)

type fakeSES struct {
	in  *sesv2.SendEmailInput
	err error
}

func (f *fakeSES) SendEmail(_ context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	f.in = in
	return &sesv2.SendEmailOutput{}, f.err
}

// Send builds a SendEmail call with the From identity, the recipient list, and
// the rendered subject + text body.
func TestSESEmailSenderSend(t *testing.T) {
	ses := &fakeSES{}
	sender := notify.NewSESEmailSender(ses, "alerts@uknomi.com")

	err := sender.Send(context.Background(), []string{"ops@example.com", "two@example.com"}, "subj", "the body")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ses.in == nil {
		t.Fatal("SendEmail was not called")
	}
	if got := aws(ses.in.FromEmailAddress); got != "alerts@uknomi.com" {
		t.Errorf("from = %q", got)
	}
	if ses.in.Destination == nil || len(ses.in.Destination.ToAddresses) != 2 ||
		ses.in.Destination.ToAddresses[0] != "ops@example.com" {
		t.Errorf("recipients = %+v", ses.in.Destination)
	}
	if ses.in.Content == nil || ses.in.Content.Simple == nil {
		t.Fatal("expected a simple email content")
	}
	if got := aws(ses.in.Content.Simple.Subject.Data); got != "subj" {
		t.Errorf("subject = %q", got)
	}
	if got := aws(ses.in.Content.Simple.Body.Text.Data); got != "the body" {
		t.Errorf("body = %q", got)
	}
}

// A SendEmail error is propagated so the fan-out records the channel failure.
func TestSESEmailSenderPropagatesError(t *testing.T) {
	ses := &fakeSES{err: errors.New("throttled")}
	sender := notify.NewSESEmailSender(ses, "alerts@uknomi.com")
	if err := sender.Send(context.Background(), []string{"a@b.c"}, "s", "b"); err == nil {
		t.Fatal("expected the SES error to propagate")
	}
}

// aws derefs an SDK *string for assertions.
func aws(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
