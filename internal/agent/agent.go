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
	"github.com/emilejacobs/control-plane/internal/handlers/cameras"
	"github.com/emilejacobs/control-plane/internal/handlers/configupdate"
	"github.com/emilejacobs/control-plane/internal/handlers/heartbeat"
	"github.com/emilejacobs/control-plane/internal/handlers/logtail"
	"github.com/emilejacobs/control-plane/internal/handlers/servicerestart"
	"github.com/emilejacobs/control-plane/internal/handlers/servicestatus"
	protologtail "github.com/emilejacobs/control-plane/internal/protocol/logtail"
	"github.com/emilejacobs/control-plane/internal/service"
	"github.com/emilejacobs/control-plane/internal/telemetry"
)

// defaultLogTailReader wraps PerOSAllowList + the kind-aware fetcher
// so the agent can register the log.tail handler without test-only
// injection. Tests pass a stub via WithLogTailReader.
//
// Kind dispatch (issue #7): "file" → TailFile; "docker" → TailDocker.
// Unknown kinds fall through with a clear CodeReadError so the
// dashboard surfaces drift rather than silently dropping the request.
type defaultLogTailReader struct{}

func (defaultLogTailReader) AllowList() map[string]protologtail.Entry { return PerOSAllowList() }
func (defaultLogTailReader) Tail(entry protologtail.Entry, lines int) (protologtail.Response, error) {
	switch entry.Kind {
	case protologtail.KindFile:
		return TailFile(entry.Target, lines, protologtail.MaxContentSize)
	case protologtail.KindDocker:
		return TailDocker(entry.Target, lines, protologtail.MaxContentSize)
	default:
		return protologtail.Response{}, &protologtail.ValidationError{
			Code:    protologtail.CodeReadError,
			Message: "unknown allow-list kind " + entry.Kind + " for " + entry.Name,
		}
	}
}

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

	// ConfigPath is the absolute path to the agent's JSON config file
	// (the same file that produced this Config via config.Load). When
	// non-empty, the Phase 2 slice 2 config.update dispatcher handler
	// is registered, allowing CP to push allow-list + cadence overrides
	// down via the cmd channel. Empty disables the downward channel.
	ConfigPath string

	// CamerasPath is the absolute path to the agent-managed cameras
	// JSON file (the downstream copy of CP's cameras inventory pushed
	// via the cameras.update cmd, per ADR-030 § 1). When non-empty,
	// the agent registers the cameras.update handler; empty disables
	// it. Suggested default in the install module:
	// /var/uknomi/agent-state/cameras.json.
	CamerasPath string
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
	logTailReader  logtail.Reader
	startTime      time.Time
	telemetry      *telemetry.Publisher
	// serviceStatus + serviceStatusCollector are set whenever a service
	// backend was supplied via WithServiceBackend (Phase 2 slice 2:
	// always constructed, even with an empty initial allow-list, so
	// config.update can hot-reload an allow-list onto an empty start).
	// Both stay nil when no backend is supplied — agent unit-tests that
	// don't need service surfaces leave them nil.
	serviceStatusCollector *telemetry.ServiceStatusCollector
	serviceStatus          *telemetry.ServiceStatusPublisher
	serviceStatusDone      chan struct{}

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

// WithLogTailReader overrides the default file-reader the log.tail
// handler delegates to. Production wires defaultLogTailReader (which
// uses PerOSAllowList + TailFile); tests pass a stub so they can
// assert against in-memory paths.
func WithLogTailReader(r logtail.Reader) Option {
	return func(a *Agent) { a.logTailReader = r }
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

	// Phase 2 slice 3: log.tail handler. Always registered when the
	// agent runs in production; tests can pass WithLogTailReader to
	// substitute a stub (or omit it to disable the handler entirely
	// for tests that don't care about the surface).
	if a.logTailReader == nil {
		a.logTailReader = defaultLogTailReader{}
	}
	a.dispatcher.Register("log.tail", logtail.New(a.logTailReader))

	// Phase 2 Edge UI rework (issue #2): cameras.update handler.
	// Registered when a cameras file path is configured. Empty path
	// disables the downward channel — tests that don't exercise
	// cameras leave it empty.
	if cfg.CamerasPath != "" {
		a.dispatcher.Register("cameras.update", cameras.New(NewCamerasApplier(cfg.CamerasPath)))
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

	// Phase 2: service-status publisher. Constructed whenever the
	// agent has a service backend, even if the initial allow-list is
	// empty — slice 2 needs the publisher running so config.update can
	// hot-reload an allow-list onto an agent that started empty.
	// Empty-list ticks produce empty Reports which cp-ingest's
	// RecordServiceStates treats as a no-op, so the operational cost
	// is a harmless 5-min heartbeat-shaped publish.
	if a.serviceBackend != nil {
		ssInterval := cfg.ServiceStatusInterval
		if ssInterval <= 0 {
			ssInterval = defaultServiceStatusInterval
		}
		a.serviceStatusCollector = &telemetry.ServiceStatusCollector{
			Backend:   a.serviceBackend,
			DeviceID:  cfg.DeviceID,
			AllowList: cfg.ServiceAllowList,
			Now:       time.Now,
			Logger:    a.logger,
		}
		a.serviceStatus = &telemetry.ServiceStatusPublisher{
			Interval:  ssInterval,
			DeviceID:  cfg.DeviceID,
			Collect:   a.serviceStatusCollector.Collect,
			Transport: transport,
			Logger:    a.logger,
		}

		// Phase 2 slice 2: register the config.update handler when a
		// config path was supplied. The Applier persists the override
		// to disk and hot-reloads the collector + publisher (ADR-028).
		if cfg.ConfigPath != "" {
			applier := NewConfigUpdateApplier(cfg.ConfigPath, a.serviceStatusCollector, a.serviceStatus)
			a.dispatcher.Register("config.update", configupdate.New(applier))
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
