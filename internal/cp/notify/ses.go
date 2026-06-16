package notify

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// sesSendAPI is the narrow SES surface the sender needs. *sesv2.Client
// satisfies it; tests inject a fake.
type sesSendAPI interface {
	SendEmail(ctx context.Context, in *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// SESEmailSender sends a digest email via Amazon SES v2 from a single verified
// identity. Implements EmailSender.
type SESEmailSender struct {
	client sesSendAPI
	from   string
}

func NewSESEmailSender(client sesSendAPI, from string) *SESEmailSender {
	return &SESEmailSender{client: client, from: from}
}

func (s *SESEmailSender) Send(ctx context.Context, recipients []string, subject, body string) error {
	_, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.from),
		Destination:      &types.Destination{ToAddresses: recipients},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: aws.String(subject)},
				Body:    &types.Body{Text: &types.Content{Data: aws.String(body)}},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("ses send: %w", err)
	}
	return nil
}
