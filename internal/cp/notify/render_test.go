package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

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
	if err := json.Unmarshal(renderTeams(d), &env); err != nil {
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
	if !strings.Contains(text, "mac-a") {
		t.Errorf("card body missing opened alert; got:\n%s", text)
	}
	if !strings.Contains(text, "mac-b") || !strings.Contains(text, "alpr") {
		t.Errorf("card body missing recovery; got:\n%s", text)
	}
}
