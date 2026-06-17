package transport_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/emilejacobs/control-plane/internal/transport"
)

// stealClientID connects a second MQTT client with the same client ID, which
// (per the MQTT spec) forces the broker to disconnect the existing client —
// the same takeover AWS IoT performs when an updated agent reconnects with the
// device's id. CleanSession=true wipes any persistent session, so the kicked
// client can only recover its subscriptions by re-subscribing on reconnect.
func stealClientID(t *testing.T, brokerURL, clientID string, certs testCerts) {
	t.Helper()
	cert, err := tls.X509KeyPair(certs.ClientCertPEM, certs.ClientKeyPEM)
	must(t, err)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certs.CAPEM)
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(clientID).
		SetTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool, MinVersion: tls.VersionTLS12}).
		SetCleanSession(true).
		SetConnectTimeout(10 * time.Second)
	c := mqtt.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(10*time.Second) || tok.Error() != nil {
		t.Fatalf("steal connect: %v", tok.Error())
	}
	time.Sleep(300 * time.Millisecond) // let the broker process the takeover
	c.Disconnect(0)                     // leave, so the transport reclaims its id
}

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

// LastPublishSuccess is the liveness signal the agent watchdog reads (#65): a
// successful connect seeds it, and every successful publish advances it. A
// wedged session (publishes failing) leaves it frozen, which the watchdog
// detects.
func TestTransportLastPublishSuccessAdvancesOnPublish(t *testing.T) {
	ctx := context.Background()
	certs := generateTestCerts(t)
	fixture := startMosquitto(t, ctx, certs)

	tr, err := transport.New(transport.Config{
		BrokerURL: fixture.BrokerURL,
		ClientID:  "liveness-client",
		CACertPEM: certs.CAPEM,
		CertPEM:   certs.ClientCertPEM,
		KeyPEM:    certs.ClientKeyPEM,
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	defer tr.Close()

	seeded := tr.LastPublishSuccess()
	if seeded.IsZero() {
		t.Fatal("expected New() to seed LastPublishSuccess on connect")
	}

	time.Sleep(5 * time.Millisecond)
	if err := tr.Publish("test/topic", []byte("x")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	advanced := tr.LastPublishSuccess()
	if !advanced.After(seeded) {
		t.Fatalf("LastPublishSuccess did not advance on a successful publish: seeded=%v advanced=%v", seeded, advanced)
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

// TestTransportResubscribesAfterReconnect is the regression for the fleet-wide
// wedge: after an MQTT reconnect (here forced by a same-client-id takeover, as
// AWS IoT does when an updated agent reconnects), the transport must still
// deliver messages on a topic it subscribed to — i.e. it re-subscribes on
// reconnect. Without that, publishes keep working but commands silently stop.
func TestTransportResubscribesAfterReconnect(t *testing.T) {
	ctx := context.Background()
	certs := generateTestCerts(t)
	fixture := startMosquitto(t, ctx, certs)

	tr, err := transport.New(transport.Config{
		BrokerURL: fixture.BrokerURL,
		ClientID:  "resub-client",
		CACertPEM: certs.CAPEM,
		CertPEM:   certs.ClientCertPEM,
		KeyPEM:    certs.ClientKeyPEM,
	})
	if err != nil {
		t.Fatalf("transport.New: %v", err)
	}
	defer tr.Close()

	received := make(chan []byte, 16)
	if err := tr.Subscribe("device/cmd", func(_ string, payload []byte) {
		received <- payload
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Sanity: the subscription delivers before any reconnect.
	if err := tr.Publish("device/cmd", []byte("before")); err != nil {
		t.Fatalf("publish before: %v", err)
	}
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery before reconnect — setup broken")
	}

	// Force the transport to drop + auto-reconnect.
	stealClientID(t, fixture.BrokerURL, "resub-client", certs)

	// After reconnect, a publish to the same topic must still be delivered.
	// Retry because the reconnect moment is non-deterministic; a publish during
	// the disconnected window simply errors and is retried.
	deadline := time.After(25 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no delivery after reconnect: the transport did not re-subscribe")
		default:
		}
		_ = tr.Publish("device/cmd", []byte("after"))
		select {
		case p := <-received:
			if string(p) == "after" {
				return // re-subscribed and delivering again
			}
		case <-time.After(500 * time.Millisecond):
		}
	}
}
