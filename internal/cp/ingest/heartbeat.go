// Package ingest holds the SQS ingest handlers that feed the Presence
// model. PresenceIngester turns a heartbeat message into a last-seen
// update; the lifecycle ingester arrives with the sweeper in #08.
package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
)

// Heartbeat is the SQS payload for a device telemetry heartbeat. device_id
// is injected by the IoT Rule (topic(2) of devices/{id}/telemetry);
// correlation_id rides in from the agent's telemetry envelope. The agent
// sends no timestamp, so last_seen is stamped at ingest.
type Heartbeat struct {
	DeviceID      string `json:"device_id"`
	CorrelationID string `json:"correlation_id"`
}

// Correlation satisfies sqsconsumer.Correlated.
func (h Heartbeat) Correlation() string { return h.CorrelationID }

// LastSeenWriter persists a device's last-seen timestamp.
// *registry.Registry satisfies it.
type LastSeenWriter interface {
	UpdateLastSeen(ctx context.Context, deviceID string, at time.Time) error
}

// PresenceIngester is the SQSConsumer handler for the heartbeat queue: it
// persists last_seen (the API's freshness source of truth) and records the
// heartbeat in the in-memory Presence model for the #08 sweeper.
type PresenceIngester struct {
	presence *presence.Presence
	writer   LastSeenWriter
	now      func() time.Time
}

// NewPresenceIngester builds the ingester. now defaults to time.Now.
func NewPresenceIngester(p *presence.Presence, w LastSeenWriter, now func() time.Time) *PresenceIngester {
	if now == nil {
		now = time.Now
	}
	return &PresenceIngester{presence: p, writer: w, now: now}
}

// Handle is the sqsconsumer.Handler[Heartbeat]. A heartbeat with no
// device_id, or one naming a device that does not exist, is a permanent
// failure — returned as poison so the consumer DLQs it rather than
// redelivering. Any other write failure is transient and redelivered.
func (i *PresenceIngester) Handle(ctx context.Context, hb Heartbeat) error {
	if hb.DeviceID == "" {
		return sqsconsumer.Poison(errors.New("heartbeat has no device_id"))
	}
	at := i.now()
	if err := i.writer.UpdateLastSeen(ctx, hb.DeviceID, at); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}
	i.presence.RecordHeartbeat(hb.DeviceID, at)
	return nil
}

// Compile-time checks that the heartbeat plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated         = Heartbeat{}
	_ sqsconsumer.Handler[Heartbeat] = (*PresenceIngester)(nil).Handle
)
