package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Config struct {
	BrokerURL string
	ClientID  string
	CACertPEM []byte
	CertPEM   []byte
	KeyPEM    []byte
}

type MessageHandler = func(topic string, payload []byte)

type Transport struct {
	client mqtt.Client

	// lastPublishOK is the time of the most recent successful publish (the
	// connect in New seeds it). It is the liveness signal the agent watchdog
	// reads to detect a wedged MQTT session (#65).
	mu            sync.Mutex
	lastPublishOK time.Time
	// subs tracks every topic the agent subscribed to, so they can be
	// re-established on each (re)connect. Without this, an auto-reconnect onto
	// a clean session silently drops the command subscription while publishes
	// keep working — the fleet-wide wedge.
	subs map[string]MessageHandler
	log  *slog.Logger
}

func New(cfg Config) (*Transport, error) {
	cert, err := tls.X509KeyPair(cfg.CertPEM, cfg.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse client cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(cfg.CACertPEM) {
		return nil, errors.New("invalid CA cert PEM")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}

	t := &Transport{subs: map[string]MessageHandler{}, log: slog.Default()}

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(cfg.ClientID).
		SetTLSConfig(tlsConfig).
		// Persistent session so the broker queues commands published during a
		// brief disconnect and delivers them on reconnect.
		SetCleanSession(false).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectTimeout(10 * time.Second).
		// Re-subscribe every tracked topic on each (re)connect. Paho fires this
		// on the initial connect and after every auto-reconnect, so a takeover
		// or network blip can no longer leave the agent deaf to commands while
		// telemetry still flows.
		SetOnConnectHandler(func(mqtt.Client) { t.resubscribeAll() })

	t.client = mqtt.NewClient(opts)
	token := t.client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		t.client.Disconnect(0)
		return nil, errors.New("connect: timeout reaching broker")
	}
	if err := token.Error(); err != nil {
		t.client.Disconnect(0)
		return nil, fmt.Errorf("connect: %w", err)
	}

	t.mu.Lock()
	t.lastPublishOK = time.Now()
	t.mu.Unlock()
	return t, nil
}

// LastPublishSuccess reports when a publish (or the initial connect) last
// succeeded. The agent watchdog (#65) treats a stale value as a wedged session.
func (t *Transport) LastPublishSuccess() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastPublishOK
}

func (t *Transport) Subscribe(topic string, h MessageHandler) error {
	t.mu.Lock()
	t.subs[topic] = h
	t.mu.Unlock()
	return t.subscribe(topic, h)
}

// subscribe issues the broker SUBSCRIBE without touching the tracked set; both
// Subscribe and the on-(re)connect resubscribe go through it.
func (t *Transport) subscribe(topic string, h MessageHandler) error {
	token := t.client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		h(msg.Topic(), msg.Payload())
	})
	token.Wait()
	return token.Error()
}

// resubscribeAll re-issues every tracked subscription. Runs from the paho
// OnConnect handler on the initial connect (a no-op while nothing is tracked
// yet) and after every auto-reconnect, restoring the command subscription a
// clean-session reconnect would otherwise have dropped.
func (t *Transport) resubscribeAll() {
	t.mu.Lock()
	subs := make(map[string]MessageHandler, len(t.subs))
	for topic, h := range t.subs {
		subs[topic] = h
	}
	t.mu.Unlock()

	for topic, h := range subs {
		if err := t.subscribe(topic, h); err != nil {
			t.log.Error("resubscribe failed", "topic", topic, "err", err)
		}
	}
}

func (t *Transport) Publish(topic string, payload []byte) error {
	token := t.client.Publish(topic, 1, false, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		return err
	}
	t.mu.Lock()
	t.lastPublishOK = time.Now()
	t.mu.Unlock()
	return nil
}

func (t *Transport) Close() error {
	t.client.Disconnect(250)
	return nil
}
