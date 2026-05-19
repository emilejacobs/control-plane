package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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

type MessageHandler func(topic string, payload []byte)

type Transport struct {
	client mqtt.Client
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

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(cfg.ClientID).
		SetTLSConfig(tlsConfig).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectTimeout(10 * time.Second)

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		client.Disconnect(0)
		return nil, errors.New("connect: timeout reaching broker")
	}
	if err := token.Error(); err != nil {
		client.Disconnect(0)
		return nil, fmt.Errorf("connect: %w", err)
	}

	return &Transport{client: client}, nil
}

func (t *Transport) Subscribe(topic string, h MessageHandler) error {
	token := t.client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		h(msg.Topic(), msg.Payload())
	})
	token.Wait()
	return token.Error()
}

func (t *Transport) Publish(topic string, payload []byte) error {
	token := t.client.Publish(topic, 1, false, payload)
	token.Wait()
	return token.Error()
}

func (t *Transport) Close() error {
	t.client.Disconnect(250)
	return nil
}
