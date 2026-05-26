// Package ingest holds cp-ingest's worker components: the SQS ingest
// handlers (PresenceIngester for heartbeats, LifecycleIngester for IoT
// connect/disconnect events) and the PresenceSweeper goroutine. All three
// feed the in-memory Presence model and persist its transitions.
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
//
// LanIP, TailscaleIP, TailscaleName were added by issue #14. All three
// are optional in the wire shape — agents that predate the rollout
// publish without them and the empty zero-value tells the ingester
// "this heartbeat omitted the field; don't write it." A non-empty
// string is the value to persist (last-wins on the column).
type Heartbeat struct {
	DeviceID      string `json:"device_id"`
	CorrelationID string `json:"correlation_id"`
	LanIP         string `json:"lan_ip,omitempty"`
	TailscaleIP   string `json:"tailscale_ip,omitempty"`
	TailscaleName string `json:"tailscale_name,omitempty"`
}

// Correlation satisfies sqsconsumer.Correlated.
func (h Heartbeat) Correlation() string { return h.CorrelationID }

// LastSeenWriter persists a device's last-seen timestamp + the
// optional per-heartbeat network fields (lan_ip + tailscale_*).
// *registry.Registry satisfies it.
//
// UpdateHeartbeatNetwork has conditional-update semantics on each
// argument: a nil *string means "don't touch this column"; a
// non-nil *string overwrites (last-wins). See
// registry.Registry.UpdateHeartbeatNetwork.
type LastSeenWriter interface {
	UpdateLastSeen(ctx context.Context, deviceID string, at time.Time) error
	UpdateHeartbeatNetwork(ctx context.Context, deviceID string, lanIP, tailscaleIP, tailscaleName *string) error
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
//
// When any of the three issue-#14 network fields is present in the
// envelope, the handler also persists them via UpdateHeartbeatNetwork
// after last_seen lands. Each present field becomes a non-nil
// *string in the call; absent fields stay nil so the registry's
// COALESCE preserves the previously stored value. When all three
// are absent (older agents) the call is skipped entirely.
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
	if hb.LanIP != "" || hb.TailscaleIP != "" || hb.TailscaleName != "" {
		if err := i.writer.UpdateHeartbeatNetwork(
			ctx, hb.DeviceID,
			optionalString(hb.LanIP),
			optionalString(hb.TailscaleIP),
			optionalString(hb.TailscaleName),
		); err != nil {
			if errors.Is(err, registry.ErrDeviceNotFound) {
				return sqsconsumer.Poison(err)
			}
			return err
		}
	}
	i.presence.RecordHeartbeat(hb.DeviceID, at)
	return nil
}

// optionalString returns nil for the empty string ("agent omitted
// this field"), a pointer to the value otherwise. The boundary
// between the JSON envelope (which uses "" to mean "absent" via
// omitempty) and the registry (which uses *string + COALESCE) sits
// here.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Compile-time checks that the heartbeat plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated         = Heartbeat{}
	_ sqsconsumer.Handler[Heartbeat] = (*PresenceIngester)(nil).Handle
)
