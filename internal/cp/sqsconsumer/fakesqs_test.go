package sqsconsumer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakeSQS is an in-memory SQS stand-in for consumer tests. A message on the
// main queue carries a receive counter; once received more than
// maxReceiveCount times without a delete, fakeSQS moves it to the DLQ —
// modelling the SQS redrive policy. A received message is invisible for
// visibilityTimeout, then becomes available again.
type fakeSQS struct {
	mu       sync.Mutex
	messages []*fakeMsg
	dlq      []string
	byHandle map[string]*fakeMsg

	maxReceiveCount   int
	visibilityTimeout time.Duration
	mainURL, dlqURL   string
	handleSeq         int
}

type fakeMsg struct {
	id        string
	body      string
	received  int
	deleted   bool
	invisible bool
}

func newFakeSQS() *fakeSQS {
	return &fakeSQS{
		byHandle:          map[string]*fakeMsg{},
		maxReceiveCount:   5,
		visibilityTimeout: 100 * time.Millisecond,
		mainURL:           "https://sqs.test/main",
		dlqURL:            "https://sqs.test/dlq",
	}
}

// seed adds a message to the main queue.
func (f *fakeSQS) seed(id, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, &fakeMsg{id: id, body: body})
}

// dlqBodies returns a snapshot of the message bodies that reached the DLQ,
// whether by explicit SendMessage or by redrive.
func (f *fakeSQS) dlqBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dlq...)
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	var out []types.Message
	limit := int(in.MaxNumberOfMessages)
	for _, m := range f.messages {
		if len(out) >= limit {
			break
		}
		if m.deleted || m.invisible {
			continue
		}
		m.received++
		if m.received > f.maxReceiveCount {
			// Redrive: the receive limit is exceeded — route to the DLQ.
			f.dlq = append(f.dlq, m.body)
			m.deleted = true
			continue
		}
		m.invisible = true
		f.handleSeq++
		handle := fmt.Sprintf("%s-h%d", m.id, f.handleSeq)
		f.byHandle[handle] = m
		msg := m
		time.AfterFunc(f.visibilityTimeout, func() {
			f.mu.Lock()
			msg.invisible = false
			f.mu.Unlock()
		})
		out = append(out, types.Message{
			MessageId:     aws.String(m.id),
			Body:          aws.String(m.body),
			ReceiptHandle: aws.String(handle),
		})
	}
	f.mu.Unlock()

	if len(out) == 0 {
		// Mimic an empty long-poll without hot-spinning the caller.
		select {
		case <-ctx.Done():
			return &sqs.ReceiveMessageOutput{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return &sqs.ReceiveMessageOutput{Messages: out}, nil
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.byHandle[aws.ToString(in.ReceiptHandle)]; m != nil {
		m.deleted = true
	}
	return &sqs.DeleteMessageOutput{}, nil
}

func (f *fakeSQS) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if aws.ToString(in.QueueUrl) == f.dlqURL {
		f.dlq = append(f.dlq, aws.ToString(in.MessageBody))
	}
	return &sqs.SendMessageOutput{MessageId: aws.String("sent")}, nil
}
