// Package sqsconsumer is a generic SQS queue consumer. It long-polls a
// queue, decodes each message body into T, validates the ADR-011
// correlation_id, hands valid payloads to a handler, and routes poison
// messages to a dead-letter queue. It is the shared ingest primitive for
// the CP Fargate workers.
package sqsconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Correlated constrains consumer payloads: every message must carry an
// ADR-011 correlation_id. A payload that decodes to an empty Correlation()
// is poison and is routed to the DLQ.
type Correlated interface {
	Correlation() string
}

// Handler processes one decoded message. Returning nil deletes the message.
// Returning a Poison-wrapped error sends it straight to the DLQ. Any other
// error leaves the message for SQS to redeliver.
type Handler[T Correlated] func(ctx context.Context, msg T) error

// ErrPoison marks a handler error as permanent: the consumer routes the
// message to the DLQ instead of leaving it for redelivery. A handler that
// can never succeed for a given message (e.g. an unknown device) returns
// Poison(cause).
var ErrPoison = errors.New("poison message")

// Poison wraps cause as a permanent failure: errors.Is(_, ErrPoison) holds.
func Poison(cause error) error {
	return fmt.Errorf("%w: %w", ErrPoison, cause)
}

// SQSClient is the subset of the AWS SQS API the consumer needs.
// *sqs.Client satisfies it.
type SQSClient interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// Config tunes a Consumer. QueueURL is required; the rest default.
type Config struct {
	QueueURL    string
	DLQURL      string
	Logger      *slog.Logger
	MaxMessages int32 // ReceiveMessage batch size; default 10
	WaitSeconds int32 // long-poll seconds; default 20
}

// Consumer long-polls QueueURL and dispatches each message to Handler.
type Consumer[T Correlated] struct {
	client      SQSClient
	handler     Handler[T]
	queueURL    string
	dlqURL      string
	log         *slog.Logger
	maxMessages int32
	waitSeconds int32
}

func NewConsumer[T Correlated](client SQSClient, handler Handler[T], cfg Config) *Consumer[T] {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = 10
	}
	if cfg.WaitSeconds == 0 {
		cfg.WaitSeconds = 20
	}
	return &Consumer[T]{
		client:      client,
		handler:     handler,
		queueURL:    cfg.QueueURL,
		dlqURL:      cfg.DLQURL,
		log:         log,
		maxMessages: cfg.MaxMessages,
		waitSeconds: cfg.WaitSeconds,
	}
}

// Run long-polls until ctx is cancelled, then waits for in-flight messages
// to finish before returning.
func (c *Consumer[T]) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for ctx.Err() == nil {
		out, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(c.queueURL),
			MaxNumberOfMessages: c.maxMessages,
			WaitTimeSeconds:     c.waitSeconds,
		})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			c.log.Error("sqs receive failed", "err", err, "queue", c.queueURL)
			continue
		}
		for _, m := range out.Messages {
			wg.Add(1)
			go func(m types.Message) {
				defer wg.Done()
				c.processOne(m)
			}(m)
		}
	}
	wg.Wait()
	c.log.Info("sqs consumer stopped", "queue", c.queueURL)
	return nil
}

// processOne decodes, validates, and dispatches a single message.
func (c *Consumer[T]) processOne(m types.Message) {
	ctx := context.Background()
	body := aws.ToString(m.Body)

	var payload T
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		c.toDLQ(ctx, m, "unparseable_json", "", err)
		return
	}
	corrID := payload.Correlation()
	if corrID == "" {
		c.toDLQ(ctx, m, "missing_correlation_id", "", nil)
		return
	}

	switch err := c.handler(ctx, payload); {
	case err == nil:
		c.deleteMsg(ctx, m)
	case errors.Is(err, ErrPoison):
		c.toDLQ(ctx, m, "handler_rejected", corrID, err)
	default:
		// Transient: leave the message. SQS makes it visible again
		// after the visibility timeout, and the queue's redrive policy
		// moves it to the DLQ once maxReceiveCount is exceeded.
		c.log.Error("handler error; leaving message for redelivery",
			"err", err, "correlation_id", corrID, "queue", c.queueURL)
	}
}

// toDLQ records the rejection, copies the message to the DLQ, and deletes it
// from the main queue so it is not reprocessed.
func (c *Consumer[T]) toDLQ(ctx context.Context, m types.Message, reason, corrID string, cause error) {
	c.log.Warn("audit.message_rejected",
		"reason", reason,
		"correlation_id", corrID,
		"cause", errString(cause),
		"queue", c.queueURL,
	)
	if c.dlqURL != "" {
		if _, err := c.client.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:    aws.String(c.dlqURL),
			MessageBody: m.Body,
		}); err != nil {
			// Leave the message on the main queue rather than lose it.
			c.log.Error("failed to send message to DLQ", "err", err, "reason", reason)
			return
		}
	}
	c.deleteMsg(ctx, m)
}

func (c *Consumer[T]) deleteMsg(ctx context.Context, m types.Message) {
	if _, err := c.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: m.ReceiptHandle,
	}); err != nil {
		c.log.Error("failed to delete message", "err", err, "queue", c.queueURL)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
