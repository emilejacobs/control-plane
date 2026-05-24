package telemetry

import (
	"context"
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
	if c.lastSeen == nil {
		c.lastSeen = map[string]observation{}
	}
	services := make([]ServiceState, 0, len(c.AllowList))
	for _, name := range c.AllowList {
		st, err := c.Backend.Status(ctx, name)
		if err != nil {
			st = service.StateUnknown
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
		CorrelationID: newCorrelationID(),
		ReportedAt:    now,
		Services:      services,
	}
}
