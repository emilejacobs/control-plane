// Package ingest holds cp-ingest's worker components: the SQS ingest
// handlers (PresenceIngester for heartbeats, LifecycleIngester for IoT
// connect/disconnect events) and the PresenceSweeper goroutine. All three
// feed the in-memory Presence model and persist its transitions.
package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	// Version is the agent's self-reported version (issue #40) — the
	// reported side of the fleet-update desired-vs-reported model. The
	// agent has always published it in the telemetry payload; empty means
	// a pre-#40 envelope and the version path is skipped.
	Version string `json:"version,omitempty"`
	// RolledBackVersion is the version the resident wrapper most recently
	// reverted (migration 024). The agent reports it from rollback.log when
	// present; empty/omitted means no rollback to record (and the previously
	// stored value is left untouched).
	RolledBackVersion string `json:"rolled_back_version,omitempty"`
	// BootTime is the device's system boot instant (RFC3339), the offline-reason
	// discriminator (#157): a changed boot_time between offline and recovery is a
	// reboot, unchanged is a network/MQTT blip. Empty/omitted = a pre-#157 agent;
	// the boot-info path is skipped. LastShutdownCause + ShutdownCauseCode are the
	// macOS previous-shutdown cause that rides with it (the code is a pointer so a
	// real 0 — power loss — is distinct from absent).
	BootTime          string `json:"boot_time,omitempty"`
	LastShutdownCause string `json:"last_shutdown_cause,omitempty"`
	ShutdownCauseCode *int   `json:"shutdown_cause_code,omitempty"`
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
	// RecordRolledBackVersion persists the version the resident wrapper most
	// recently reverted on the device (migration 024), reported in the
	// heartbeat. Called only when the field is present.
	RecordRolledBackVersion(ctx context.Context, deviceID, version string) error
	// RecordReportedAgentVersion persists the heartbeat-reported agent
	// version and returns the device's desired version (nil =
	// untargeted) so the ingester can make the reconcile decision in
	// the same round trip.
	RecordReportedAgentVersion(ctx context.Context, deviceID, version string) (desired *string, err error)
	// RecordBootInfo records the device's reported system boot time + shutdown
	// cause (#157). When the boot_time differs from the device's stored value
	// (including first contact) it inserts a device_reboots history row and
	// updates the device's last boot state; an unchanged boot_time is a no-op.
	// code is nil when the agent had no shutdown cause to report. at is the
	// ingest clock (detected_at).
	RecordBootInfo(ctx context.Context, deviceID string, bootTime time.Time, cause string, code *int, at time.Time) error
}

// UpdatePusher re-publishes agent.update toward a device whose reported
// version drifted from desired (issue #40, ADR-035 §1).
// *agentrollout.Pusher satisfies it.
type UpdatePusher interface {
	Push(ctx context.Context, deviceID, version, correlationID string) error
}

// PresenceIngester is the SQSConsumer handler for the heartbeat queue: it
// persists last_seen (the API's freshness source of truth) and records the
// heartbeat in the in-memory Presence model for the #08 sweeper.
type PresenceIngester struct {
	presence *presence.Presence
	writer   LastSeenWriter
	now      func() time.Time

	// Updates, when non-nil, enables the issue-#40 reconcile path: a
	// heartbeat reporting a version != the device's desired version
	// re-pushes agent.update. nil (cp-ingest without AGENT_DIST_BUCKET)
	// still persists the reported version, just never pushes.
	Updates UpdatePusher
	// Logger receives reconcile push failures (the next heartbeat
	// retries; a push failure never fails heartbeat ingestion). nil
	// discards.
	Logger *slog.Logger
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
	if hb.BootTime != "" {
		// Best-effort: a malformed boot_time is dropped (logged), never fatal —
		// the rest of the heartbeat (last_seen, etc.) is fine and the next
		// heartbeat carries the same static value.
		if bt, err := time.Parse(time.RFC3339, hb.BootTime); err != nil {
			i.logger().Warn("heartbeat boot_time unparseable; skipping boot info",
				"device_id", hb.DeviceID, "boot_time", hb.BootTime, "err", err)
		} else if err := i.writer.RecordBootInfo(ctx, hb.DeviceID, bt, hb.LastShutdownCause, hb.ShutdownCauseCode, at); err != nil {
			if errors.Is(err, registry.ErrDeviceNotFound) {
				return sqsconsumer.Poison(err)
			}
			return err
		}
	}
	if hb.RolledBackVersion != "" {
		if err := i.writer.RecordRolledBackVersion(ctx, hb.DeviceID, hb.RolledBackVersion); err != nil {
			if errors.Is(err, registry.ErrDeviceNotFound) {
				return sqsconsumer.Poison(err)
			}
			return err
		}
	}
	if hb.Version != "" {
		desired, err := i.writer.RecordReportedAgentVersion(ctx, hb.DeviceID, hb.Version)
		if err != nil {
			if errors.Is(err, registry.ErrDeviceNotFound) {
				return sqsconsumer.Poison(err)
			}
			return err
		}
		// Reconcile (issue #40): reported != desired means this device
		// missed (or failed) the rollout push — re-push. Push failure is
		// logged, not returned: the heartbeat itself ingested fine and
		// the next heartbeat retries the push.
		if i.Updates != nil && desired != nil && *desired != hb.Version {
			if err := i.Updates.Push(ctx, hb.DeviceID, *desired, hb.CorrelationID); err != nil {
				i.logger().Warn("reconcile push failed; next heartbeat retries",
					"device_id", hb.DeviceID, "desired", *desired,
					"reported", hb.Version, "err", err)
			}
		}
	}
	i.presence.RecordHeartbeat(hb.DeviceID, at)
	return nil
}

func (i *PresenceIngester) logger() *slog.Logger {
	if i.Logger != nil {
		return i.Logger
	}
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
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
