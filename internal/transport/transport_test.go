package transport_test

import (
	"context"
	"testing"
	"time"

	"github.com/uknomi/control-plane/internal/transport"
)

func TestTransportRoundtrip(t *testing.T) {
	ctx := context.Background()
	certs := generateTestCerts(t)
	fixture := startMosquitto(t, ctx, certs)

	tr, err := transport.New(transport.Config{
		BrokerURL: fixture.BrokerURL,
		ClientID:  "test-client",
		CACertPEM: certs.CAPEM,
		CertPEM:   certs.ClientCertPEM,
		KeyPEM:    certs.ClientKeyPEM,
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	defer tr.Close()

	received := make(chan []byte, 1)
	if err := tr.Subscribe("test/topic", func(topic string, payload []byte) {
		received <- payload
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := tr.Publish("test/topic", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case payload := <-received:
		if string(payload) != "hello" {
			t.Fatalf("payload: got %q, want %q", payload, "hello")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestTransportNewFailsWhenBrokerUnreachable(t *testing.T) {
	certs := generateTestCerts(t)

	_, err := transport.New(transport.Config{
		BrokerURL: "tls://127.0.0.1:1", // no broker on port 1
		ClientID:  "unreachable-client",
		CACertPEM: certs.CAPEM,
		CertPEM:   certs.ClientCertPEM,
		KeyPEM:    certs.ClientKeyPEM,
	})

	if err == nil {
		t.Fatal("expected error when broker is unreachable, got nil")
	}
}

// Auto-reconnect after broker disappearance is verified via field deployment
// (Issue 07/08: "deliberate network blip → agent reconnects"). An automated
// test was attempted here using container stop/start, but colima does not
// preserve the host port mapping across restarts, so Paho cannot find the
// broker on its old address. A toxiproxy-based fault injector would solve
// this; deferred until a future cycle if the field test surfaces issues.
