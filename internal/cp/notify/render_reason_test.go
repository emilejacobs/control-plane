package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// A recovered OFFLINE alert carrying an offline reason (#158) renders the reason
// on the recovery line — "reboot: clean restart" vs "network blip" — in both the
// Teams card and the email body, so an operator learns the cause without opening
// the dashboard.
func TestRenderOfflineReasonOnRecovery(t *testing.T) {
	d := ingest.Digest{
		Resolved: []ingest.AlertEvent{
			{Kind: registry.UnhealthyOffline, DeviceID: "dev-a", Hostname: "mac-a", Reason: "reboot: clean restart"},
			{Kind: registry.UnhealthyOffline, DeviceID: "dev-b", Hostname: "mac-b", Reason: "network blip"},
		},
	}

	teams := teamsText(t, d)
	if !strings.Contains(teams, "reboot: clean restart") {
		t.Errorf("teams missing reboot reason:\n%s", teams)
	}
	if !strings.Contains(teams, "network blip") {
		t.Errorf("teams missing network-blip reason:\n%s", teams)
	}

	_, body := renderEmail(d, "https://cp.test")
	if !strings.Contains(body, "reboot: clean restart") || !strings.Contains(body, "network blip") {
		t.Errorf("email body missing offline reason:\n%s", body)
	}
}

// A device on an old agent (no boot_time reported) recovers with no reason set —
// the recovery line stays clean with no false cause appended.
func TestRenderNoReasonWhenUnknown(t *testing.T) {
	d := ingest.Digest{
		Resolved: []ingest.AlertEvent{
			{Kind: registry.UnhealthyOffline, DeviceID: "dev-a", Hostname: "mac-a"}, // Reason == ""
		},
	}
	_, body := renderEmail(d, "https://cp.test")
	// The line is just "OFFLINE · mac-a — <url>"; no trailing reason artifacts.
	if strings.Contains(body, "reboot") || strings.Contains(body, "network blip") {
		t.Errorf("unknown reason should render nothing; got:\n%s", body)
	}
}

func teamsText(t *testing.T, d ingest.Digest) string {
	t.Helper()
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
	return card.String()
}
