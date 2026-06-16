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
// A simple {"text": markdown} body is the lowest-common-denominator shape a
// Power Automate "Post to a channel" flow accepts; the markdown groups new
// alerts and recoveries.
func renderTeams(d ingest.Digest) []byte {
	openedTotal := len(d.Opened) + d.Truncated
	var b strings.Builder
	fmt.Fprintf(&b, "**uKnomi fleet:** %d new alert(s), %d recovered\n", openedTotal, len(d.Resolved))
	for _, e := range d.Opened {
		b.WriteString("\n🔴 " + describe(e))
	}
	if d.Truncated > 0 {
		fmt.Fprintf(&b, "\n🔴 …and %d more", d.Truncated)
	}
	for _, e := range d.Resolved {
		b.WriteString("\n🟢 " + describe(e))
	}
	payload, _ := json.Marshal(map[string]string{"text": b.String()})
	return payload
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
