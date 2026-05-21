package telemetry_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/telemetry"
)

type fakeTransport struct {
	mu        sync.Mutex
	published map[string][][]byte
	gotOne    chan struct{}
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		published: make(map[string][][]byte),
		gotOne:    make(chan struct{}, 1),
	}
}

func (f *fakeTransport) Publish(topic string, payload []byte) error {
	f.mu.Lock()
	f.published[topic] = append(f.published[topic], payload)
	f.mu.Unlock()
	select {
	case f.gotOne <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeTransport) snapshot(topic string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.published[topic]))
	copy(out, f.published[topic])
	return out
}

func TestPublisherEmitsOneTick(t *testing.T) {
	tr := newFakeTransport()

	p := &telemetry.Publisher{
		Interval: 5 * time.Millisecond,
		DeviceID: "dev-test",
		Collectors: []func() map[string]any{
			func() map[string]any { return map[string]any{"foo": "bar"} },
		},
		Transport: tr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	select {
	case <-tr.gotOne:
	case <-time.After(time.Second):
		t.Fatal("no publish within 1s")
	}
	cancel()
	<-done

	publishes := tr.snapshot("devices/dev-test/telemetry")
	if len(publishes) == 0 {
		t.Fatal("expected at least one publish on devices/dev-test/telemetry")
	}

	var payload map[string]any
	if err := json.Unmarshal(publishes[0], &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload["foo"] != "bar" {
		t.Errorf("payload.foo: got %v, want bar", payload["foo"])
	}
	corr, ok := payload["correlation_id"].(string)
	if !ok || corr == "" {
		t.Errorf("correlation_id missing or empty: %v", payload["correlation_id"])
	}
}

func TestPublisherSurvivesPanickingCollector(t *testing.T) {
	tr := newFakeTransport()

	p := &telemetry.Publisher{
		Interval: 5 * time.Millisecond,
		DeviceID: "dev-test",
		Collectors: []func() map[string]any{
			func() map[string]any { panic("collector boom") },
			func() map[string]any { return map[string]any{"healthy": true} },
		},
		Transport: tr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	// Wait for at least two ticks to prove the second one fires after the first's panic.
	deadline := time.After(time.Second)
	for {
		if len(tr.snapshot("devices/dev-test/telemetry")) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d publishes within 1s — publisher likely died on the panic", len(tr.snapshot("devices/dev-test/telemetry")))
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	<-done

	publishes := tr.snapshot("devices/dev-test/telemetry")
	for i, raw := range publishes {
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("publish %d not JSON: %v", i, err)
		}
		if payload["healthy"] != true {
			t.Errorf("publish %d missing healthy=true from good collector: %v", i, payload)
		}
	}
}
