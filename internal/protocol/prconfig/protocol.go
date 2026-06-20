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

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// regionPattern matches a Plate Recognizer region code like "us-az" or "fr".
var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)*$`)

// Validate checks a Config for the API PUT surface: region must look like a PR
// region code, camera_id must be set, and each webhook needs a name and an
// http(s) URL. The dashboard constrains region via a dropdown; this is the
// server-side backstop.
func Validate(c Config) error {
	if !regionPattern.MatchString(c.Region) {
		return fmt.Errorf("invalid region %q (expected a code like \"us-az\")", c.Region)
	}
	if strings.TrimSpace(c.CameraID) == "" {
		return errors.New("camera_id is required")
	}
	for i, wh := range c.Webhooks {
		if strings.TrimSpace(wh.Name) == "" {
			return fmt.Errorf("webhook %d: name is required", i)
		}
		u, err := url.Parse(wh.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("webhook %q: url must be http(s)", wh.Name)
		}
	}
	return nil
}

// Webhook is one inline webhook target. image/caching are per-webhook (matching
// PR's config.ini schema); enabled maps to membership in [cameras].webhook_targets.
// The webhook registry (#6) will normalise these later.
type Webhook struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Image   bool   `json:"image"`
	Caching bool   `json:"caching"`
}

// Config is the wire shape for a device's CP-managed PR config — used in the
// GET/PUT API bodies. LastAppliedAt/LastAppliedCorrID are read-only audit
// fields the registry stamps on apply-ack (set by the agent round-trip).
type Config struct {
	CameraID          string     `json:"camera_id"`
	Region            string     `json:"region"`
	Webhooks          []Webhook  `json:"webhooks"`
	LastAppliedAt     *time.Time `json:"last_applied_at,omitempty"`
	LastAppliedCorrID string     `json:"last_applied_corr_id,omitempty"`
}
