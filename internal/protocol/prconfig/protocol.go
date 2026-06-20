// Package prconfig holds the wire types and validation for the per-device
// Plate Recognizer config surface (issue #5, ADR-030 § 3). Both the CP-side
// API handlers and the agent-side pr.config.update handler depend on this
// package so the two halves can't drift on what a valid config looks like.
//
// CP stores the editable SUBSET (region, camera_id, caching, image, webhooks);
// the agent merges it into the on-disk config.ini, preserving fields not
// modelled here. The LPR camera RTSP URL is resolved server-side from the
// cameras inventory at push time, not stored here.
package prconfig

import "time"

// Webhook is one inline webhook target ([{name,url,enabled}]). The webhook
// registry (#6) will normalise these later.
type Webhook struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// Config is the wire shape for a device's CP-managed PR config — used in the
// GET/PUT API bodies. LastAppliedAt/LastAppliedCorrID are read-only audit
// fields the registry stamps on apply-ack (set by the agent round-trip).
type Config struct {
	CameraID          string     `json:"camera_id"`
	Region            string     `json:"region"`
	Caching           bool       `json:"caching"`
	Image             bool       `json:"image"`
	Webhooks          []Webhook  `json:"webhooks"`
	LastAppliedAt     *time.Time `json:"last_applied_at,omitempty"`
	LastAppliedCorrID string     `json:"last_applied_corr_id,omitempty"`
}
