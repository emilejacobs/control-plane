package ingest

import (
	"context"
	"errors"
	"fmt"
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

// LifecycleIngester is the SQSConsumer handler for the IoT lifecycle queue:
// it maps connected/disconnected events to a stored presence flip and to the
// in-memory Presence model — the fast-path online↔offline edge that does not
// wait for the sweeper.
type LifecycleIngester struct {
	presence *presence.Presence
	writer   PresenceWriter
	now      func() time.Time
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
	} else {
		i.presence.OnDisconnect(ev.ClientID, at)
	}
	return nil
}

// Compile-time checks that the lifecycle plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated         = Lifecycle{}
	_ sqsconsumer.Handler[Lifecycle] = (*LifecycleIngester)(nil).Handle
)
