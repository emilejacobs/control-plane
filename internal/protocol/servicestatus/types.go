// Package servicestatus defines the wire types for the per-device
// service-status reporting flow (Phase 2). The agent's
// telemetry.ServiceStatusCollector produces a Report; cp-ingest's
// service-status handler consumes the same shape after the IoT Rule
// → SQS hop.
//
// This package is deliberately tiny: just the types. Behaviour lives
// on the producer (internal/telemetry) and consumer (internal/cp/ingest)
// sides. Sharing the struct here keeps both halves in lockstep so a
// schema change to the JSON payload is a single edit, not two.
package servicestatus

import (
	"time"

	"github.com/emilejacobs/control-plane/internal/service"
)

// Report is the JSON payload published on devices/{id}/service-status.
type Report struct {
	DeviceID      string         `json:"device_id"`
	CorrelationID string         `json:"correlation_id"`
	ReportedAt    time.Time      `json:"reported_at"`
	Services      []ServiceState `json:"services"`
}

// Correlation satisfies sqsconsumer.Correlated on the cp-ingest side.
// Defining it here keeps the wire shape and the consumer contract in
// one place; cp-ingest does not need to wrap the type.
func (r Report) Correlation() string { return r.CorrelationID }

// ServiceState is the per-service entry in Report.Services. State_since
// is best-effort and resets on agent restart — see the producer
// (internal/telemetry.ServiceStatusCollector) for the tracking story.
type ServiceState struct {
	Name       string        `json:"name"`
	State      service.State `json:"state"`
	StateSince time.Time     `json:"state_since"`
}
