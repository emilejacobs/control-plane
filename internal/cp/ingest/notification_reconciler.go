package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// NotificationStore is the registry slice the reconciler reads and writes:
// the system-actor fleet-unhealthy snapshot plus the alert_state CRUD.
// *registry.Registry satisfies it.
type NotificationStore interface {
	FleetUnhealthy(ctx context.Context) ([]registry.UnhealthySignal, error)
	LoadOpenAlerts(ctx context.Context) ([]registry.OpenAlert, error)
	OpenAlert(ctx context.Context, kind registry.UnhealthyKind, deviceID, subject string, at time.Time) error
	MarkAlertNotified(ctx context.Context, kind registry.UnhealthyKind, deviceID, subject string, at time.Time) error
	ResolveAlert(ctx context.Context, kind registry.UnhealthyKind, deviceID, subject string, at time.Time) error
}

// AlertEvent is one alert transition rendered into a digest — enough to name
// the device and the signal that tripped (or cleared) without a second lookup.
type AlertEvent struct {
	Kind     registry.UnhealthyKind
	DeviceID string
	Subject  string
	Hostname string
	SiteName *string
}

// Digest is the coalesced set of transitions found in one reconcile tick. It is
// delivered to each channel once, so a fleet-wide event is a single message.
// Truncated counts opened events beyond the per-tick cap that were summarized
// rather than enumerated.
type Digest struct {
	Opened    []AlertEvent
	Resolved  []AlertEvent
	Truncated int
}

// Empty reports whether the digest carries nothing to deliver.
func (d Digest) Empty() bool {
	return len(d.Opened) == 0 && len(d.Resolved) == 0 && d.Truncated == 0
}

// Notifier delivers a digest. Implementations fan out to the configured
// channels (#98); the reconciler treats a returned error as a delivery failure
// and leaves the affected alerts un-notified so the next tick retries.
type Notifier interface {
	Notify(ctx context.Context, d Digest) error
}

// NotificationReconciler diffs the live fleet-unhealthy snapshot against the
// open alert_state rows each tick and fires transition-only notifications. It
// mirrors PresenceSweeper: a goroutine on a ticker, idempotent per tick (a
// still-unhealthy signal produces nothing because its alert is already open
// and notified).
type NotificationReconciler struct {
	store    NotificationStore
	notifier Notifier
	log      *slog.Logger
	interval time.Duration
	cap      int
	now      func() time.Time
}

// NotificationReconcilerConfig tunes a NotificationReconciler. All fields
// default.
type NotificationReconcilerConfig struct {
	Interval time.Duration // tick interval; default 1 min
	Cap      int           // max opened events enumerated per digest; default 25
	Logger   *slog.Logger
	Now      func() time.Time
}

func NewNotificationReconciler(store NotificationStore, notifier Notifier, cfg NotificationReconcilerConfig) *NotificationReconciler {
	interval := cfg.Interval
	if interval == 0 {
		interval = time.Minute
	}
	capN := cfg.Cap
	if capN == 0 {
		capN = 25
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &NotificationReconciler{store: store, notifier: notifier, log: log, interval: interval, cap: capN, now: now}
}

// Run reconciles on every interval tick until ctx is cancelled.
func (r *NotificationReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.log.Info("notification reconciler stopped")
			return
		case <-ticker.C:
			if err := r.ReconcileOnce(ctx); err != nil {
				r.log.Error("notification reconcile failed", "err", err)
			}
		}
	}
}

// alertIdentity keys an alert by (kind, device, subject).
type alertIdentity struct {
	kind     registry.UnhealthyKind
	deviceID string
	subject  string
}

func signalIdentity(s registry.UnhealthySignal) alertIdentity {
	return alertIdentity{s.Kind, s.DeviceID, s.Subject}
}

func openIdentity(a registry.OpenAlert) alertIdentity {
	return alertIdentity{a.Kind, a.DeviceID, a.Subject}
}

// ReconcileOnce runs one diff pass: it loads the fleet-unhealthy snapshot and
// the open alerts, builds the digest of transitions, delivers it, and persists
// the resulting alert_state changes. Exported so tests can drive a single tick
// deterministically.
func (r *NotificationReconciler) ReconcileOnce(ctx context.Context) error {
	now := r.now()

	snapshot, err := r.store.FleetUnhealthy(ctx)
	if err != nil {
		return err
	}
	openRows, err := r.store.LoadOpenAlerts(ctx)
	if err != nil {
		return err
	}

	openByID := make(map[alertIdentity]registry.OpenAlert, len(openRows))
	for _, a := range openRows {
		openByID[openIdentity(a)] = a
	}

	// "Owed" = a snapshot signal with no open row yet, or one whose open row
	// has never been notified (a prior send failed). Both need a notification.
	var owed []registry.UnhealthySignal
	for _, s := range snapshot {
		a, ok := openByID[signalIdentity(s)]
		if !ok || a.LastNotifiedAt == nil {
			owed = append(owed, s)
		}
	}

	digest := Digest{}
	for i, s := range owed {
		if i < r.cap {
			digest.Opened = append(digest.Opened, eventFromSignal(s))
		} else {
			digest.Truncated++
		}
	}

	if digest.Empty() {
		r.log.Info("notify.tick", "opened", 0, "resolved", 0)
		return nil
	}

	if err := r.notifier.Notify(ctx, digest); err != nil {
		// Delivery failed: still record opened_at for brand-new alerts so the
		// detection time is captured, but do NOT mark them notified — the next
		// tick re-detects them as owed and retries.
		for _, s := range owed {
			_ = r.store.OpenAlert(ctx, s.Kind, s.DeviceID, s.Subject, now)
		}
		r.log.Error("notify.tick delivery failed", "owed", len(owed), "err", err)
		return nil
	}

	for _, s := range owed {
		if err := r.store.OpenAlert(ctx, s.Kind, s.DeviceID, s.Subject, now); err != nil {
			r.log.Error("open alert failed", "device_id", s.DeviceID, "err", err)
			continue
		}
		if err := r.store.MarkAlertNotified(ctx, s.Kind, s.DeviceID, s.Subject, now); err != nil {
			r.log.Error("mark notified failed", "device_id", s.DeviceID, "err", err)
		}
	}
	r.log.Info("notify.tick", "opened", len(owed), "resolved", 0, "truncated", digest.Truncated)
	return nil
}

func eventFromSignal(s registry.UnhealthySignal) AlertEvent {
	return AlertEvent{
		Kind:     s.Kind,
		DeviceID: s.DeviceID,
		Subject:  s.Subject,
		Hostname: s.Hostname,
		SiteName: s.SiteName,
	}
}
