package notify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// renderEmail produces the subject + plain-text body for a digest. The subject
// summarizes counts; the body lists each opened alert and recovery so an
// operator can triage from their inbox without opening the dashboard.
func renderEmail(d ingest.Digest) (subject, body string) {
	openedTotal := len(d.Opened) + d.Truncated
	subject = fmt.Sprintf("uKnomi fleet: %d new alert(s), %d recovered", openedTotal, len(d.Resolved))

	var b strings.Builder
	if len(d.Opened) > 0 || d.Truncated > 0 {
		b.WriteString("New alerts:\n")
		for _, e := range d.Opened {
			b.WriteString("  - " + describe(e) + "\n")
		}
		if d.Truncated > 0 {
			fmt.Fprintf(&b, "  - …and %d more\n", d.Truncated)
		}
	}
	if len(d.Resolved) > 0 {
		b.WriteString("\nRecovered:\n")
		for _, e := range d.Resolved {
			b.WriteString("  - " + describe(e) + "\n")
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
func renderTeams(d ingest.Digest) []byte {
	openedTotal := len(d.Opened) + d.Truncated

	body := []map[string]any{{
		"type":   "TextBlock",
		"text":   fmt.Sprintf("uKnomi fleet: %d new alert(s), %d recovered", openedTotal, len(d.Resolved)),
		"weight": "Bolder",
		"size":   "Medium",
		"wrap":   true,
	}}
	for _, e := range d.Opened {
		body = append(body, textBlock("🔴 "+describe(e), "Attention"))
	}
	if d.Truncated > 0 {
		body = append(body, textBlock(fmt.Sprintf("🔴 …and %d more", d.Truncated), "Attention"))
	}
	for _, e := range d.Resolved {
		body = append(body, textBlock("🟢 "+describe(e), "Good"))
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

// describe renders one alert event as a human line: kind, device (hostname or
// id), optional subject, optional site.
func describe(e ingest.AlertEvent) string {
	device := e.Hostname
	if device == "" {
		device = e.DeviceID
	}
	parts := []string{kindLabel(e.Kind), device}
	if e.Subject != "" {
		parts = append(parts, e.Subject)
	}
	line := strings.Join(parts, " · ")
	if e.SiteName != nil && *e.SiteName != "" {
		line += " (" + *e.SiteName + ")"
	}
	return line
}

func kindLabel(k registry.UnhealthyKind) string {
	switch k {
	case registry.UnhealthyOffline:
		return "OFFLINE"
	case registry.UnhealthyServiceStopped:
		return "SERVICE STOPPED"
	case registry.UnhealthyProbeRed:
		return "PROBE RED"
	default:
		return string(k)
	}
}
