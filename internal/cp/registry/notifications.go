package registry

import (
	"context"
	"fmt"
	"time"
)

// UnhealthyKind is the category of a fleet-unhealthy signal. The three v1
// notification kinds; the string values are the alert_state.kind column and
// are part of the alert identity, so they are stable.
type UnhealthyKind string

const (
	// UnhealthyOffline — a device whose stored presence flag is false.
	UnhealthyOffline UnhealthyKind = "offline"
	// UnhealthyServiceStopped — a device reporting a service in state 'stopped'.
	UnhealthyServiceStopped UnhealthyKind = "service_stopped"
	// UnhealthyProbeRed — a device red on a health probe (yellow is excluded
	// from notifications; it is dashboard-only).
	UnhealthyProbeRed UnhealthyKind = "probe_red"
	// UnhealthyCameraOffline — a camera whose reachability status is offline
	// (#114, PRD #111). Subject is the camera_id (stable identity); the
	// camera label rides separately in Label for the rendered alert line.
	UnhealthyCameraOffline UnhealthyKind = "camera_offline"
)

// UnhealthySignal is one entry in the fleet-wide unhealthy snapshot. The
// identity that dedupes against alert_state is (Kind, DeviceID, Subject);
// Subject is the service/probe name and is empty for an offline signal.
// Hostname and SiteName are carried for rendering the notification — SiteName
// is nil for an unassigned device.
type UnhealthySignal struct {
	Kind     UnhealthyKind
	DeviceID string
	Subject  string
	Hostname string
	SiteName *string
	// Label is a human display name for the subject, used by rendering when
	// the raw subject isn't operator-friendly. Currently the camera label for
	// camera_offline signals; empty for the other kinds (which render the
	// subject directly).
	Label string
}

// FleetUnhealthy returns the whole fleet's current unhealthy signals — offline
// devices, stopped services, and red probes — as a flat, deterministically
// ordered list. Unlike FleetAlerts (#21), it applies NO operator site filter:
// the notification reconciler is a system actor that must see every site, so
// this read deliberately bypasses authz scoping rather than failing closed on
// an absent SiteFilter. Used only by cp-ingest's NotificationReconciler.
func (r *Registry) FleetUnhealthy(ctx context.Context) ([]UnhealthySignal, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT 'offline' AS kind, d.id::text AS device_id, '' AS subject,
		       d.hostname, s.name AS site_name, '' AS label
		FROM devices d
		LEFT JOIN sites s ON s.id = d.site_id
		WHERE d.is_online = false
		UNION ALL
		SELECT 'service_stopped', d.id::text, ds.service_name,
		       d.hostname, s.name, ''
		FROM device_services ds
		JOIN devices d ON d.id = ds.device_id
		LEFT JOIN sites s ON s.id = d.site_id
		WHERE ds.state = 'stopped'
		UNION ALL
		SELECT 'probe_red', d.id::text, dhp.probe_name,
		       d.hostname, s.name, ''
		FROM device_health_probes dhp
		JOIN devices d ON d.id = dhp.device_id
		LEFT JOIN sites s ON s.id = d.site_id
		WHERE dhp.status = 'red'
		UNION ALL
		SELECT 'camera_offline', d.id::text, dc.camera_id,
		       d.hostname, s.name, dc.label
		FROM device_cameras dc
		JOIN devices d ON d.id = dc.device_id
		LEFT JOIN sites s ON s.id = d.site_id
		WHERE dc.status = 'offline'
		ORDER BY kind, hostname, subject
	`)
	if err != nil {
		return nil, fmt.Errorf("query fleet unhealthy: %w", err)
	}
	defer rows.Close()

	var out []UnhealthySignal
	for rows.Next() {
		var sig UnhealthySignal
		var kind string
		if err := rows.Scan(&kind, &sig.DeviceID, &sig.Subject, &sig.Hostname, &sig.SiteName, &sig.Label); err != nil {
			return nil, fmt.Errorf("scan fleet unhealthy: %w", err)
		}
		sig.Kind = UnhealthyKind(kind)
		out = append(out, sig)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fleet unhealthy: %w", err)
	}
	return out, nil
}

// OpenAlert is one currently-open row in alert_state (resolved_at IS NULL).
// LastNotifiedAt is nil until a digest carrying this alert has been delivered;
// the reconciler treats a nil LastNotifiedAt as "still owed a notification" so
// a failed send is retried on the next tick.
type OpenAlert struct {
	Kind           UnhealthyKind
	DeviceID       string
	Subject        string
	OpenedAt       time.Time
	LastNotifiedAt *time.Time
	NotifyAttempts int
	// Hostname and SiteName are joined from the device row so a recovery notice
	// names the device the way an operator recognizes it (not the raw id).
	// SiteName is nil for an unassigned device.
	Hostname string
	SiteName *string
	// Label is the camera label for a camera_offline row, joined from
	// device_cameras so a recovery notice can name the camera the same way the
	// fire did. Empty for other kinds, or for a camera deleted before recovery
	// (render falls back to the subject/camera_id).
	Label string
}

// LoadOpenAlerts returns every open alert_state row (resolved_at IS NULL). The
// reconciler diffs these against the FleetUnhealthy snapshot to decide what to
// fire, recover, or leave alone.
func (r *Registry) LoadOpenAlerts(ctx context.Context) ([]OpenAlert, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.kind, a.device_id::text, a.subject, a.opened_at, a.last_notified_at, a.notify_attempts,
		       d.hostname, s.name AS site_name,
		       COALESCE(dc.label, '') AS label
		FROM alert_state a
		JOIN devices d ON d.id = a.device_id
		LEFT JOIN sites s ON s.id = d.site_id
		LEFT JOIN device_cameras dc
		       ON a.kind = 'camera_offline' AND dc.device_id = a.device_id AND dc.camera_id = a.subject
		WHERE a.resolved_at IS NULL
		ORDER BY a.kind, a.device_id, a.subject
	`)
	if err != nil {
		return nil, fmt.Errorf("query open alerts: %w", err)
	}
	defer rows.Close()

	var out []OpenAlert
	for rows.Next() {
		var a OpenAlert
		var kind string
		if err := rows.Scan(&kind, &a.DeviceID, &a.Subject, &a.OpenedAt, &a.LastNotifiedAt, &a.NotifyAttempts,
			&a.Hostname, &a.SiteName, &a.Label); err != nil {
			return nil, fmt.Errorf("scan open alert: %w", err)
		}
		a.Kind = UnhealthyKind(kind)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open alerts: %w", err)
	}
	return out, nil
}

// OpenAlert inserts a new open alert for the given identity, stamped opened_at.
// It is idempotent: if an open row for (kind, device_id, subject) already
// exists, the partial unique index makes this a no-op, so a re-detected signal
// never duplicates. last_notified_at starts NULL — the row is owed its first
// notification.
func (r *Registry) OpenAlert(ctx context.Context, kind UnhealthyKind, deviceID, subject string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO alert_state (kind, device_id, subject, opened_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (kind, device_id, subject) WHERE resolved_at IS NULL DO NOTHING
	`, string(kind), deviceID, subject, at)
	if err != nil {
		return fmt.Errorf("open alert: %w", err)
	}
	return nil
}

// MarkAlertNotified records that the open alert for this identity has been
// delivered: it stamps last_notified_at and increments notify_attempts. Called
// only after a successful send, so an un-stamped row signals a pending retry.
func (r *Registry) MarkAlertNotified(ctx context.Context, kind UnhealthyKind, deviceID, subject string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE alert_state
		SET last_notified_at = $4, notify_attempts = notify_attempts + 1
		WHERE kind = $1 AND device_id = $2 AND subject = $3 AND resolved_at IS NULL
	`, string(kind), deviceID, subject, at)
	if err != nil {
		return fmt.Errorf("mark alert notified: %w", err)
	}
	return nil
}

// ResolveAlert closes the open alert for this identity, stamping resolved_at.
// The row is retained as history; a later recurrence opens a fresh row via
// OpenAlert. A no-op if no open row matches (already resolved / never opened).
func (r *Registry) ResolveAlert(ctx context.Context, kind UnhealthyKind, deviceID, subject string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE alert_state
		SET resolved_at = $4
		WHERE kind = $1 AND device_id = $2 AND subject = $3 AND resolved_at IS NULL
	`, string(kind), deviceID, subject, at)
	if err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	return nil
}
