package notify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// renderEmail produces the subject + plain-text body for a digest. The subject
// summarizes counts; the body lists each opened alert and recovery (with the
// device's CP link) so an operator can triage from their inbox.
func renderEmail(d ingest.Digest, baseURL string) (subject, body string) {
	openedTotal := len(d.Opened) + d.Truncated
	subject = fmt.Sprintf("uKnomi fleet: %d new alert(s), %d recovered", openedTotal, len(d.Resolved))

	var b strings.Builder
	if len(d.Opened) > 0 || d.Truncated > 0 {
		b.WriteString("New alerts:\n")
		for _, e := range d.Opened {
			b.WriteString("  - " + describePlain(e, baseURL) + "\n")
		}
		if d.Truncated > 0 {
			fmt.Fprintf(&b, "  - …and %d more\n", d.Truncated)
		}
	}
	if len(d.Resolved) > 0 {
		b.WriteString("\nRecovered:\n")
		for _, e := range d.Resolved {
			b.WriteString("  - " + describePlain(e, baseURL) + "\n")
		}
	}
	return subject, b.String()
}

// renderTeams produces the JSON payload posted to the Teams Workflows webhook.
// The Power Automate "Workflows" trigger that replaced the legacy Office 365
// incoming webhook expects an **adaptive-card attachment envelope**, not the
// old {"text": …} connector body — it 202s any JSON, so the wrong shape posts
// nothing to the channel. Each alert is one TextBlock, coloured Attention (new)
// or Good (recovered).
func renderTeams(d ingest.Digest, baseURL string) []byte {
	openedTotal := len(d.Opened) + d.Truncated

	body := []map[string]any{{
		"type":   "TextBlock",
		"text":   fmt.Sprintf("uKnomi fleet: %d new alert(s), %d recovered", openedTotal, len(d.Resolved)),
		"weight": "Bolder",
		"size":   "Medium",
		"wrap":   true,
	}}
	for _, e := range d.Opened {
		body = append(body, textBlock("🔴 "+describeMarkdown(e, baseURL), "Attention"))
	}
	if d.Truncated > 0 {
		body = append(body, textBlock(fmt.Sprintf("🔴 …and %d more", d.Truncated), "Attention"))
	}
	for _, e := range d.Resolved {
		body = append(body, textBlock("🟢 "+describeMarkdown(e, baseURL), "Good"))
	}

	card := map[string]any{
		"type":    "AdaptiveCard",
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"version": "1.4",
		"body":    body,
	}
	payload, _ := json.Marshal(map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content":     card,
		}},
	})
	return payload
}

// textBlock is one wrapped, coloured adaptive-card TextBlock.
func textBlock(text, color string) map[string]any {
	return map[string]any{"type": "TextBlock", "text": text, "wrap": true, "color": color}
}

// describeMarkdown renders one alert event for an adaptive-card TextBlock, with
// the device name as a Markdown link to its CP page: kind · [name](url) ·
// subject (site).
func describeMarkdown(e ingest.AlertEvent, baseURL string) string {
	device := deviceLabel(e)
	if u := deviceURL(e, baseURL); u != "" {
		device = "[" + device + "](" + u + ")"
	}
	return joinAlert(e, device)
}

// describePlain renders one alert event as a plain-text line (email), appending
// the device's CP link so mail clients linkify it: kind · name · subject (site)
// — url.
func describePlain(e ingest.AlertEvent, baseURL string) string {
	line := joinAlert(e, deviceLabel(e))
	if u := deviceURL(e, baseURL); u != "" {
		line += " — " + u
	}
	return line
}

// joinAlert assembles "kind · <device> · subject (site)" given an already-
// formatted device token (plain name or Markdown link). The subject slot
// prefers the operator-friendly Label (the camera label for camera_offline)
// over the raw Subject (the camera_id) when one is set.
func joinAlert(e ingest.AlertEvent, device string) string {
	parts := []string{kindLabel(e.Kind), device}
	if subj := subjectLabel(e); subj != "" {
		parts = append(parts, subj)
	}
	// Offline-recovery reason (#158): "reboot: <cause>" / "network blip". Set
	// only on resolved offline events; empty otherwise so nothing is appended.
	if e.Reason != "" {
		parts = append(parts, e.Reason)
	}
	line := strings.Join(parts, " · ")
	if e.SiteName != nil && *e.SiteName != "" {
		line += " (" + *e.SiteName + ")"
	}
	return line
}

// subjectLabel is the human-facing subject token: the Label when set (camera
// label for camera_offline), otherwise the raw Subject.
func subjectLabel(e ingest.AlertEvent) string {
	if e.Label != "" {
		return e.Label
	}
	return e.Subject
}

// deviceLabel is the device's hostname, falling back to its id.
func deviceLabel(e ingest.AlertEvent) string {
	if e.Hostname != "" {
		return e.Hostname
	}
	return e.DeviceID
}

// deviceURL is the device's CP page, or "" when no base URL is configured.
func deviceURL(e ingest.AlertEvent, baseURL string) string {
	if baseURL == "" || e.DeviceID == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/devices/" + e.DeviceID
}

func kindLabel(k registry.UnhealthyKind) string {
	switch k {
	case registry.UnhealthyOffline:
		return "OFFLINE"
	case registry.UnhealthyServiceStopped:
		return "SERVICE STOPPED"
	case registry.UnhealthyProbeRed:
		return "PROBE RED"
	case registry.UnhealthyCameraOffline:
		return "CAMERA OFFLINE"
	default:
		return string(k)
	}
}
