package agent

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/emilejacobs/control-plane/internal/dispatcher"
	"github.com/emilejacobs/control-plane/internal/handlers/heartbeat"
	"github.com/emilejacobs/control-plane/internal/handlers/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

type Config struct {
	CertPath string
	DeviceID string
	Version  string
}

type Transport interface {
	Subscribe(topic string, handler func(topic string, payload []byte)) error
	Publish(topic string, payload []byte) error
	Close() error
}

type Agent struct {
	transport      Transport
	dispatcher     *dispatcher.Dispatcher
	deviceID       string
	logger         *slog.Logger
	serviceBackend service.Backend
}

type Option func(*Agent)

func WithLogger(l *slog.Logger) Option {
	return func(a *Agent) { a.logger = l }
}

func WithServiceBackend(b service.Backend) Option {
	return func(a *Agent) { a.serviceBackend = b }
}

func New(cfg Config, transport Transport, opts ...Option) (*Agent, error) {
	if err := validateCertFile(cfg.CertPath); err != nil {
		return nil, err
	}

	a := &Agent{
		transport: transport,
		deviceID:  cfg.DeviceID,
		logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(a)
	}

	a.dispatcher = dispatcher.New(dispatcher.WithLogger(a.logger))
	a.dispatcher.Register("heartbeat", heartbeat.New(cfg.DeviceID, cfg.Version, time.Now()))
	if a.serviceBackend != nil {
		a.dispatcher.Register("service.status", servicestatus.New(a.serviceBackend))
	}

	return a, nil
}

func (a *Agent) Start() error {
	cmdTopic := "devices/" + a.deviceID + "/cmd"
	resultTopic := "devices/" + a.deviceID + "/cmd-result"

	return a.transport.Subscribe(cmdTopic, func(_ string, payload []byte) {
		resultBytes, err := a.dispatcher.Dispatch(context.Background(), payload)
		if err != nil {
			a.logger.Error("dispatch failed", "error", err)
			return
		}
		if err := a.transport.Publish(resultTopic, resultBytes); err != nil {
			a.logger.Error("publish result failed", "error", err)
		}
	})
}

func (a *Agent) Stop() error {
	return a.transport.Close()
}

func validateCertFile(path string) error {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cert file %s: %w", path, err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return fmt.Errorf("cert file %s: not a valid PEM block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("cert file %s: %w", path, err)
	}
	if time.Now().After(cert.NotAfter) {
		return fmt.Errorf("cert file %s: expired at %s", path, cert.NotAfter.Format(time.RFC3339))
	}
	return nil
}
