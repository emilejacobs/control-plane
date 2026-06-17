package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// A camera_offline alert renders the CAMERA OFFLINE label, the device-name CP
// link, and the camera LABEL (not the raw camera_id) as the subject, with the
// site — in both the Teams card and the email body.
func TestRenderCameraOfflineLine(t *testing.T) {
	site := "Acme Downtown"
	d := ingest.Digest{
		Opened: []ingest.AlertEvent{{
			Kind:     registry.UnhealthyCameraOffline,
			DeviceID: "dev-a",
			Subject:  "cam1", // stable identity (camera_id)
			Label:    "Drive-thru",
			Hostname: "mac-a",
			SiteName: &site,
		}},
	}

	// Teams adaptive card.
	var env struct {
		Attachments []struct {
			Content struct {
				Body []struct {
					Text string `json:"text"`
				} `json:"body"`
			} `json:"content"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(renderTeams(d, "https://cp.test"), &env); err != nil {
		t.Fatalf("teams payload not valid JSON: %v", err)
	}
	var card strings.Builder
	for _, b := range env.Attachments[0].Content.Body {
		card.WriteString(b.Text + "\n")
	}
	teams := card.String()
	if !strings.Contains(teams, "CAMERA OFFLINE") {
		t.Errorf("teams missing CAMERA OFFLINE label:\n%s", teams)
	}
	if !strings.Contains(teams, "[mac-a](https://cp.test/devices/dev-a)") {
		t.Errorf("teams missing device-name CP link:\n%s", teams)
	}
	if !strings.Contains(teams, "Drive-thru") {
		t.Errorf("teams should show the camera label:\n%s", teams)
	}
	if strings.Contains(teams, "· cam1") {
		t.Errorf("teams should show the label, not the raw camera_id:\n%s", teams)
	}
	if !strings.Contains(teams, "(Acme Downtown)") {
		t.Errorf("teams missing site:\n%s", teams)
	}

	// Email body.
	_, body := renderEmail(d, "https://cp.test")
	if !strings.Contains(body, "CAMERA OFFLINE") || !strings.Contains(body, "Drive-thru") ||
		!strings.Contains(body, "https://cp.test/devices/dev-a") || !strings.Contains(body, "(Acme Downtown)") {
		t.Errorf("email body missing camera-offline content:\n%s", body)
	}
	if strings.Contains(body, "· cam1") {
		t.Errorf("email should show the label, not the raw camera_id:\n%s", body)
	}
}

// renderTeams must emit the MS Teams Workflows adaptive-card envelope —
// {"type":"message","attachments":[{contentType: adaptive, content: card}]} —
// not the legacy {"text":...} connector shape. The Workflows trigger 202s any
// JSON, so the wrong shape posts nothing to the channel (the bug this locks).
func TestRenderTeamsAdaptiveCardEnvelope(t *testing.T) {
	d := ingest.Digest{
		Opened: []ingest.AlertEvent{
			{Kind: registry.UnhealthyOffline, DeviceID: "dev-a", Hostname: "mac-a"},
		},
		Resolved: []ingest.AlertEvent{
			{Kind: registry.UnhealthyServiceStopped, DeviceID: "dev-b", Hostname: "mac-b", Subject: "alpr"},
		},
	}

	var env struct {
		Type        string `json:"type"`
		Attachments []struct {
			ContentType string `json:"contentType"`
			Content     struct {
				Type string `json:"type"`
				Body []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"body"`
			} `json:"content"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(renderTeams(d, "https://cp.test"), &env); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	if env.Type != "message" {
		t.Errorf("envelope type = %q, want %q", env.Type, "message")
	}
	if len(env.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(env.Attachments))
	}
	att := env.Attachments[0]
	if att.ContentType != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("contentType = %q", att.ContentType)
	}
	if att.Content.Type != "AdaptiveCard" {
		t.Errorf("content type = %q, want AdaptiveCard", att.Content.Type)
	}
	// The opened + recovered lines must appear somewhere in the card body.
	var all strings.Builder
	for _, b := range att.Content.Body {
		all.WriteString(b.Text + "\n")
	}
	text := all.String()
	// Both the opened and the recovered alert name the device by hostname
	// (not the raw id) as a Markdown link to its CP page.
	if !strings.Contains(text, "[mac-a](https://cp.test/devices/dev-a)") {
		t.Errorf("opened alert missing device-name link; got:\n%s", text)
	}
	if !strings.Contains(text, "[mac-b](https://cp.test/devices/dev-b)") || !strings.Contains(text, "alpr") {
		t.Errorf("recovery missing device-name link; got:\n%s", text)
	}
	// The raw id must not leak as the visible label when a hostname is known.
	if strings.Contains(text, "🟢 SERVICE STOPPED · dev-b ·") {
		t.Errorf("recovery shows id instead of hostname; got:\n%s", text)
	}
}
