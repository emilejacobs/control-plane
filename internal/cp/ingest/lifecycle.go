package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
)

// Lifecycle is the SQS payload for an IoT Core presence lifecycle event.
// clientId is the MQTT client id, which for a device agent equals its
// device_id. correlation_id is injected by the lifecycle IoT Rule
// (newuuid()) — AWS lifecycle events do not carry one of their own.
type Lifecycle struct {
	ClientID      string `json:"clientId"`
	EventType     string `json:"eventType"`
	CorrelationID string `json:"correlation_id"`
}

// Correlation satisfies sqsconsumer.Correlated.
func (l Lifecycle) Correlation() string { return l.CorrelationID }

// PresenceWriter persists a presence transition. *registry.Registry
// satisfies it.
type PresenceWriter interface {
	SetPresence(ctx context.Context, deviceID string, online bool, at time.Time) error
}

// AgentVersionReader looks up a device's reported + desired agent version
// for the reconnect reconcile check (issue #40). *registry.Registry
// satisfies it.
type AgentVersionReader interface {
	AgentVersionState(ctx context.Context, deviceID string) (reported string, desired *string, err error)
}

// LifecycleIngester is the SQSConsumer handler for the IoT lifecycle queue:
// it maps connected/disconnected events to a stored presence flip and to the
// in-memory Presence model — the fast-path online↔offline edge that does not
// wait for the sweeper.
type LifecycleIngester struct {
	presence *presence.Presence
	writer   PresenceWriter
	now      func() time.Time

	// Versions + Updates, when both non-nil, enable the issue-#40
	// reconnect reconcile: a device that comes back online still on the
	// wrong version gets agent.update re-pushed — this is how an offline
	// device converges on a rollout it missed. Reconcile failures are
	// logged, never returned: the presence flip already persisted, and
	// the device's own heartbeats retry the reconcile path.
	Versions AgentVersionReader
	Updates  UpdatePusher
	// Logger receives reconcile failures. nil discards.
	Logger *slog.Logger
}

// NewLifecycleIngester builds the ingester. now defaults to time.Now.
func NewLifecycleIngester(p *presence.Presence, w PresenceWriter, now func() time.Time) *LifecycleIngester {
	if now == nil {
		now = time.Now
	}
	return &LifecycleIngester{presence: p, writer: w, now: now}
}

// Handle is the sqsconsumer.Handler[Lifecycle]. A missing clientId, an
// unrecognised eventType, or a clientId that names no device is permanent
// failure — returned as poison so the consumer DLQs it. The presence row is
// persisted before the in-memory model is touched, so an unknown device
// never leaves a stray in-memory entry.
func (i *LifecycleIngester) Handle(ctx context.Context, ev Lifecycle) error {
	if ev.ClientID == "" {
		return sqsconsumer.Poison(errors.New("lifecycle event has no clientId"))
	}

	var online bool
	switch ev.EventType {
	case "connected":
		online = true
	case "disconnected":
		online = false
	default:
		return sqsconsumer.Poison(fmt.Errorf("unknown lifecycle eventType %q", ev.EventType))
	}

	at := i.now()
	if err := i.writer.SetPresence(ctx, ev.ClientID, online, at); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}

	if online {
		i.presence.OnConnect(ev.ClientID, at)
		i.reconcile(ctx, ev)
	} else {
		i.presence.OnDisconnect(ev.ClientID, at)
	}
	return nil
}

// reconcile re-pushes agent.update to a freshly-connected device whose
// reported version drifted from desired (issue #40, ADR-035 §1).
func (i *LifecycleIngester) reconcile(ctx context.Context, ev Lifecycle) {
	if i.Versions == nil || i.Updates == nil {
		return
	}
	reported, desired, err := i.Versions.AgentVersionState(ctx, ev.ClientID)
	if err != nil {
		i.logger().Warn("reconcile version lookup failed",
			"device_id", ev.ClientID, "err", err)
		return
	}
	if desired == nil || *desired == reported {
		return
	}
	if err := i.Updates.Push(ctx, ev.ClientID, *desired, ev.CorrelationID); err != nil {
		i.logger().Warn("reconcile push on reconnect failed; heartbeats retry",
			"device_id", ev.ClientID, "desired", *desired,
			"reported", reported, "err", err)
	}
}

func (i *LifecycleIngester) logger() *slog.Logger {
	if i.Logger != nil {
		return i.Logger
	}
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// Compile-time checks that the lifecycle plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated         = Lifecycle{}
	_ sqsconsumer.Handler[Lifecycle] = (*LifecycleIngester)(nil).Handle
)
