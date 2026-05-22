package sqsconsumer

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// testPayload is a minimal Correlated payload for consumer tests.
type testPayload struct {
	CorrelationID string `json:"correlation_id"`
	DeviceID      string `json:"device_id"`
}

func (p testPayload) Correlation() string { return p.CorrelationID }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// runConsumer starts c.Run in a goroutine and returns a stop func that
// cancels it and waits for Run to return.
func runConsumer[T Correlated](t *testing.T, c *Consumer[T]) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("consumer did not stop within 2s")
		}
	}
}

// waitFor polls cond up to 2s, failing the test if it never holds.
func waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", desc)
}

func TestConsumerHappyPath(t *testing.T) {
	fake := newFakeSQS()
	fake.seed("m1", `{"correlation_id":"corr-1","device_id":"dev-1"}`)

	got := make(chan testPayload, 1)
	handler := func(_ context.Context, msg testPayload) error {
		got <- msg
		return nil
	}
	c := NewConsumer[testPayload](fake, handler, Config{
		QueueURL: fake.mainURL, DLQURL: fake.dlqURL, Logger: discardLogger(),
	})
	stop := runConsumer(t, c)
	defer stop()

	select {
	case msg := <-got:
		if msg.CorrelationID != "corr-1" || msg.DeviceID != "dev-1" {
			t.Errorf("handler got %+v, want corr-1/dev-1", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler not invoked within 2s")
	}

	waitFor(t, "message deleted from the main queue", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.messages[0].deleted
	})
	if dlq := fake.dlqBodies(); len(dlq) != 0 {
		t.Errorf("DLQ should be empty, got %v", dlq)
	}
}

func TestConsumerMalformedJSONToDLQ(t *testing.T) {
	fake := newFakeSQS()
	fake.seed("m1", `{not valid json`)

	var mu sync.Mutex
	called := false
	handler := func(_ context.Context, _ testPayload) error {
		mu.Lock()
		called = true
		mu.Unlock()
		return nil
	}
	c := NewConsumer[testPayload](fake, handler, Config{
		QueueURL: fake.mainURL, DLQURL: fake.dlqURL, Logger: discardLogger(),
	})
	stop := runConsumer(t, c)
	defer stop()

	waitFor(t, "malformed message routed to DLQ", func() bool {
		return len(fake.dlqBodies()) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if called {
		t.Error("handler was invoked for a malformed message")
	}
}

func TestConsumerMissingCorrelationIDToDLQ(t *testing.T) {
	fake := newFakeSQS()
	fake.seed("m1", `{"device_id":"dev-1"}`) // valid JSON, no correlation_id

	var mu sync.Mutex
	called := false
	handler := func(_ context.Context, _ testPayload) error {
		mu.Lock()
		called = true
		mu.Unlock()
		return nil
	}
	c := NewConsumer[testPayload](fake, handler, Config{
		QueueURL: fake.mainURL, DLQURL: fake.dlqURL, Logger: discardLogger(),
	})
	stop := runConsumer(t, c)
	defer stop()

	waitFor(t, "message missing correlation_id routed to DLQ", func() bool {
		return len(fake.dlqBodies()) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if called {
		t.Error("handler was invoked for a message missing correlation_id")
	}
	if dlq := fake.dlqBodies(); dlq[0] != `{"device_id":"dev-1"}` {
		t.Errorf("DLQ body: got %q, want the original message", dlq[0])
	}
}
