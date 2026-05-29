package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/emilejacobs/control-plane/internal/probes"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// ProbeReport is the wire type for the fleet-health-probes flow, shared
// with cp-ingest via internal/protocol/healthprobes. Aliased here so
// telemetry callers keep the familiar package.Type spelling.
type ProbeReport = healthprobes.Report

// ProbeCollector runs the OS-specific probes.Backend and wraps the
// results in a Report stamped with a fresh correlation_id and the
// current time. It does not loop — ProbePublisher drives the cadence.
type ProbeCollector struct {
	Backend  probes.Backend
	DeviceID string
	Now      func() time.Time
	Logger   *slog.Logger
}

// Collect runs every probe once and returns a stamped Report.
func (c *ProbeCollector) Collect(ctx context.Context) healthprobes.Report {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	return healthprobes.Report{
		DeviceID:      c.DeviceID,
		CorrelationID: newCorrelationID(),
		ReportedAt:    now(),
		Probes:        c.Backend.Collect(ctx),
	}
}

// ProbePublisher drives a ProbeCollector on an Interval ticker and
// publishes each Report as JSON on devices/{DeviceID}/health-probes.
// Mirrors ServiceStatusPublisher.
type ProbePublisher struct {
	Interval  time.Duration
	DeviceID  string
	Collect   func(context.Context) healthprobes.Report
	Transport Transport
	Logger    *slog.Logger

	mu     sync.Mutex
	ticker *time.Ticker
}

// SetInterval updates the cadence; if Run is active the ticker resets.
func (p *ProbePublisher) SetInterval(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Interval = d
	if p.ticker != nil {
		p.ticker.Reset(d)
	}
}

// Run blocks until ctx is cancelled, publishing on every Interval tick.
func (p *ProbePublisher) Run(ctx context.Context) {
	log := p.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	p.mu.Lock()
	p.ticker = time.NewTicker(p.Interval)
	ticker := p.ticker
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.ticker.Stop()
		p.ticker = nil
		p.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.publishOnce(ctx, log)
		}
	}
}

func (p *ProbePublisher) publishOnce(ctx context.Context, log *slog.Logger) {
	report := p.Collect(ctx)
	body, err := json.Marshal(report)
	if err != nil {
		log.Error("health-probes marshal failed", "error", err, "correlation_id", report.CorrelationID)
		return
	}
	topic := "devices/" + p.DeviceID + "/health-probes"
	if err := p.Transport.Publish(topic, body); err != nil {
		log.Error("health-probes publish failed", "error", err, "correlation_id", report.CorrelationID, "topic", topic)
	}
}
