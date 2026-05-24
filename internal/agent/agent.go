package agent

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/emilejacobs/control-plane/internal/dispatcher"
	"github.com/emilejacobs/control-plane/internal/handlers/heartbeat"
	"github.com/emilejacobs/control-plane/internal/handlers/servicerestart"
	"github.com/emilejacobs/control-plane/internal/handlers/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
	"github.com/emilejacobs/control-plane/internal/telemetry"
)

const (
	defaultTelemetryInterval     = 30 * time.Second
	defaultServiceStatusInterval = 5 * time.Minute
)

type Config struct {
	CertPath          string
	DeviceID          string
	Version           string
	TelemetryInterval time.Duration

	// ServiceAllowList enables the Phase 2 service-status reporter when
	// non-empty. Names are launchd unit names on Mac, systemd unit names
	// on Linux. ServiceStatusInterval defaults to 5 minutes when unset.
	ServiceAllowList      []string
	ServiceStatusInterval time.Duration
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
	version        string
	logger         *slog.Logger
	serviceBackend service.Backend
	startTime      time.Time
	telemetry      *telemetry.Publisher
	// serviceStatus is set only when cfg.ServiceAllowList is non-empty
	// AND a service backend was supplied via WithServiceBackend. nil
	// otherwise — Start treats that as "feature disabled".
	serviceStatus     *telemetry.ServiceStatusPublisher
	serviceStatusDone chan struct{}

	pubCancel context.CancelFunc
	pubDone   chan struct{}
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
		version:   cfg.Version,
		logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		startTime: time.Now(),
	}
	for _, opt := range opts {
		opt(a)
	}

	a.dispatcher = dispatcher.New(dispatcher.WithLogger(a.logger))
	a.dispatcher.Register("heartbeat", heartbeat.New(cfg.DeviceID, cfg.Version, a.startTime))
	if a.serviceBackend != nil {
		a.dispatcher.Register("service.status", servicestatus.New(a.serviceBackend))
		a.dispatcher.Register("service.restart", servicerestart.New(a.serviceBackend))
	}

	interval := cfg.TelemetryInterval
	if interval <= 0 {
		interval = defaultTelemetryInterval
	}
	a.telemetry = &telemetry.Publisher{
		Interval:   interval,
		DeviceID:   cfg.DeviceID,
		Collectors: a.defaultCollectors(),
		Transport:  transport,
		Logger:     a.logger,
	}

	// Phase 2: optional service-status publisher. Skipped silently when
	// ServiceAllowList is empty OR when no service backend was provided
	// (the agent unit-tests construct a stub backend; production cmd/agent
	// always wires the real one).
	if len(cfg.ServiceAllowList) > 0 && a.serviceBackend != nil {
		ssInterval := cfg.ServiceStatusInterval
		if ssInterval <= 0 {
			ssInterval = defaultServiceStatusInterval
		}
		collector := &telemetry.ServiceStatusCollector{
			Backend:   a.serviceBackend,
			DeviceID:  cfg.DeviceID,
			AllowList: cfg.ServiceAllowList,
			Now:       time.Now,
			Logger:    a.logger,
		}
		a.serviceStatus = &telemetry.ServiceStatusPublisher{
			Interval:  ssInterval,
			DeviceID:  cfg.DeviceID,
			Collect:   collector.Collect,
			Transport: transport,
			Logger:    a.logger,
		}
	}

	return a, nil
}

func (a *Agent) defaultCollectors() []func() map[string]any {
	return []func() map[string]any{
		func() map[string]any {
			return map[string]any{
				"device_id":      a.deviceID,
				"version":        a.version,
				"os":             runtime.GOOS,
				"uptime_seconds": int64(time.Since(a.startTime).Seconds()),
			}
		},
		func() map[string]any {
			last := a.dispatcher.LastCommandAt()
			if last.IsZero() {
				return map[string]any{"last_command_at": nil}
			}
			return map[string]any{"last_command_at": last.UTC().Format(time.RFC3339)}
		},
	}
}

func (a *Agent) Start() error {
	cmdTopic := "devices/" + a.deviceID + "/cmd"
	resultTopic := "devices/" + a.deviceID + "/cmd-result"

	if err := a.transport.Subscribe(cmdTopic, func(_ string, payload []byte) {
		resultBytes, err := a.dispatcher.Dispatch(context.Background(), payload)
		if err != nil {
			a.logger.Error("dispatch failed", "error", err)
			return
		}
		if err := a.transport.Publish(resultTopic, resultBytes); err != nil {
			a.logger.Error("publish result failed", "error", err)
		}
	}); err != nil {
		return err
	}

	pubCtx, cancel := context.WithCancel(context.Background())
	a.pubCancel = cancel
	a.pubDone = make(chan struct{})
	go func() {
		defer close(a.pubDone)
		a.telemetry.Run(pubCtx)
	}()

	if a.serviceStatus != nil {
		a.serviceStatusDone = make(chan struct{})
		go func() {
			defer close(a.serviceStatusDone)
			a.serviceStatus.Run(pubCtx)
		}()
	}
	return nil
}

func (a *Agent) Stop() error {
	if a.pubCancel != nil {
		a.pubCancel()
		<-a.pubDone
		if a.serviceStatusDone != nil {
			<-a.serviceStatusDone
		}
	}
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
