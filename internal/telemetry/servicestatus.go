package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/service"
)

// Report is the JSON payload published on devices/{id}/service-status.
// Cp-ingest consumes it as the typed shape; the agent's
// ServiceStatusCollector produces it.
type Report struct {
	DeviceID      string         `json:"device_id"`
	CorrelationID string         `json:"correlation_id"`
	ReportedAt    time.Time      `json:"reported_at"`
	Services      []ServiceState `json:"services"`
}

// ServiceState is the per-service entry in Report.Services. State_since is
// best-effort: the collector tracks last-observed state in memory across
// calls, so a process restart resets state_since to "now" for every
// service. Authoritative since-tracking would need disk persistence or
// OS-level support that's inconsistent across launchd/systemd.
type ServiceState struct {
	Name       string        `json:"name"`
	State      service.State `json:"state"`
	StateSince time.Time     `json:"state_since"`
}

// ServiceStatusCollector queries service.Backend for each name in
// AllowList and produces a Report. It does not loop on its own — the
// caller (ServiceStatusPublisher) drives the cadence.
//
// Collect is NOT goroutine-safe; the publisher owns the cadence and
// must not call Collect concurrently.
type ServiceStatusCollector struct {
	Backend   service.Backend
	DeviceID  string
	AllowList []string
	Now       func() time.Time
	// Logger receives a warn-level line for every Backend.Status error
	// other than ErrNotFound (which is the expected "service not loaded"
	// case and stays quiet). Optional; nil defaults to a discard logger.
	Logger *slog.Logger

	// lastSeen memoises (state, since) per service name so that StateSince
	// only advances when the observed state actually changes. Reset on
	// process restart — see ServiceState doc.
	lastSeen map[string]observation
}

type observation struct {
	state service.State
	since time.Time
}

// Collect runs Status against every allow-listed name and returns a
// Report stamped with a fresh correlation_id and the current time.
func (c *ServiceStatusCollector) Collect(ctx context.Context) Report {
	now := c.Now()
	log := c.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if c.lastSeen == nil {
		c.lastSeen = map[string]observation{}
	}
	correlationID := newCorrelationID()
	services := make([]ServiceState, 0, len(c.AllowList))
	for _, name := range c.AllowList {
		st, err := c.Backend.Status(ctx, name)
		if err != nil {
			st = service.StateUnknown
			if !errors.Is(err, service.ErrNotFound) {
				log.Warn("service status query failed",
					"service", name,
					"error", err.Error(),
					"correlation_id", correlationID,
				)
			}
		}
		prev, seen := c.lastSeen[name]
		since := now
		if seen && prev.state == st {
			since = prev.since
		}
		c.lastSeen[name] = observation{state: st, since: since}
		services = append(services, ServiceState{
			Name:       name,
			State:      st,
			StateSince: since,
		})
	}
	return Report{
		DeviceID:      c.DeviceID,
		CorrelationID: correlationID,
		ReportedAt:    now,
		Services:      services,
	}
}

// ServiceStatusPublisher drives a ServiceStatusCollector (passed in via
// the Collect func) on an Interval ticker and publishes each Report as
// JSON on devices/{DeviceID}/service-status. Mirrors Publisher's shape
// but carries a typed payload so cp-ingest can deserialize cleanly.
type ServiceStatusPublisher struct {
	Interval  time.Duration
	DeviceID  string
	Collect   func(context.Context) Report
	Transport Transport
	Logger    *slog.Logger
}

// Run blocks until ctx is cancelled, publishing on every Interval tick.
func (p *ServiceStatusPublisher) Run(ctx context.Context) {
	log := p.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.publishOnce(ctx, log)
		}
	}
}

func (p *ServiceStatusPublisher) publishOnce(ctx context.Context, log *slog.Logger) {
	report := p.Collect(ctx)
	body, err := json.Marshal(report)
	if err != nil {
		log.Error("service-status marshal failed", "error", err, "correlation_id", report.CorrelationID)
		return
	}
	topic := "devices/" + p.DeviceID + "/service-status"
	if err := p.Transport.Publish(topic, body); err != nil {
		log.Error("service-status publish failed", "error", err, "correlation_id", report.CorrelationID, "topic", topic)
	}
}
