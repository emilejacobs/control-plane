package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

// Report and ServiceState are the wire types for the service-status
// reporting flow. They live in internal/protocol/servicestatus so the
// agent (producer) and cp-ingest (consumer) share one definition.
// Re-exported here as type aliases so existing telemetry callers keep
// the familiar package.Type spelling.
type (
	Report       = servicestatus.Report
	ServiceState = servicestatus.ServiceState
)

// ServiceStatusCollector queries service.Backend for each name in
// AllowList and produces a Report. It does not loop on its own — the
// caller (ServiceStatusPublisher) drives the cadence.
//
// Collect and SetAllowList are mutually serialised by an internal
// mutex so the cmd.update flow can swap the list mid-run (Phase 2 slice
// 2). Direct mutation of the AllowList field after first Collect is
// not supported — use SetAllowList.
type ServiceStatusCollector struct {
	Backend   service.Backend
	DeviceID  string
	AllowList []string
	Now       func() time.Time
	// Logger receives a warn-level line for every Backend.Status error
	// other than ErrNotFound (which is the expected "service not loaded"
	// case and stays quiet). Optional; nil defaults to a discard logger.
	Logger *slog.Logger

	mu sync.Mutex
	// lastSeen memoises (state, since) per service name so that StateSince
	// only advances when the observed state actually changes. Reset on
	// process restart — see ServiceState doc.
	lastSeen map[string]observation
}

// SetAllowList replaces the collector's allow-list. lastSeen entries
// for services no longer in the list are dropped so a re-add starts
// fresh rather than reporting a stale "running since" stamp from the
// prior membership window.
func (c *ServiceStatusCollector) SetAllowList(list []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AllowList = list
	if c.lastSeen == nil {
		return
	}
	keep := make(map[string]struct{}, len(list))
	for _, n := range list {
		keep[n] = struct{}{}
	}
	for name := range c.lastSeen {
		if _, ok := keep[name]; !ok {
			delete(c.lastSeen, name)
		}
	}
}

type observation struct {
	state service.State
	since time.Time
}

// Collect runs Status against every allow-listed name and returns a
// Report stamped with a fresh correlation_id and the current time.
func (c *ServiceStatusCollector) Collect(ctx context.Context) Report {
	c.mu.Lock()
	defer c.mu.Unlock()
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
//
// SetInterval can change the cadence mid-run; the active ticker is
// reset to fire at the new rate on the next tick (Phase 2 slice 2
// config.update flow).
type ServiceStatusPublisher struct {
	Interval  time.Duration
	DeviceID  string
	Collect   func(context.Context) Report
	Transport Transport
	Logger    *slog.Logger

	mu     sync.Mutex
	ticker *time.Ticker // nil until Run starts
}

// SetInterval updates the publisher's cadence. If Run is active, the
// underlying ticker is reset to the new duration; the next tick fires
// at most d after the call (modulo Go's ticker semantics). If Run has
// not yet been called, the field is updated and the ticker will use it
// at start.
func (p *ServiceStatusPublisher) SetInterval(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Interval = d
	if p.ticker != nil {
		p.ticker.Reset(d)
	}
}

// Run blocks until ctx is cancelled, publishing on every Interval tick.
func (p *ServiceStatusPublisher) Run(ctx context.Context) {
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
