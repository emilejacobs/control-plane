package prconfigini

import (
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

// realConfig mirrors a live captured PR Stream config.ini (creds redacted) —
// the merge must update CP-managed keys and preserve everything else.
const realConfig = `timezone = UTC
config_change_restart = yes
[cameras]
    regions = us-az
    sample = 2
    mmc = yes
    detection_rule = normal
    webhook_targets = prod, pre-prod
    report_static = no
    [[66_3]]
        url = rtsp://user:pass@192.168.43.6:554/profile2/media.smp
        active = yes
[webhooks]
    [[prod]]
        url = https://api-flask.uknomi.com/recognize_vehicle_event
        name = prod
        image = no
        header = MAC: 1cf64c54a756
        caching = no
        image_quality = 80
    [[pre-prod]]
        url = https://preprod-flask.uknomi.com/recognize_vehicle_event
        name = pre-prod
        image = no
        header = MAC: 1cf64c54a756
        caching = no
        image_quality = 80
`

func TestMergePreservesAndUpdates(t *testing.T) {
	cfg := prconfig.Config{
		CameraID: "66_3",
		Region:   "us-ca", // changed
		Webhooks: []prconfig.Webhook{
			{Name: "prod", URL: "https://api-flask.uknomi.com/recognize_vehicle_event", Enabled: true, Image: true, Caching: false},
			{Name: "pre-prod", URL: "https://preprod-flask.uknomi.com/recognize_vehicle_event", Enabled: false, Image: false, Caching: false},
		},
	}
	out, err := Merge([]byte(realConfig), cfg, "rtsp://user:pass@192.168.43.6:554/profile2/media.smp")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	s := string(out)

	mustContain := []string{
		"regions = us-ca",                          // region updated
		"webhook_targets = prod",                   // only prod enabled
		"header = MAC: 1cf64c54a756",               // device-specific field preserved
		"sample = 2", "mmc = yes", "detection_rule = normal", // unmodeled [cameras] preserved
		"image_quality = 80",                       // unmodeled per-webhook preserved
		"[[66_3]]",                                 // camera section intact
		"active = yes",
	}
	for _, want := range mustContain {
		if !strings.Contains(s, want) {
			t.Errorf("merged output missing %q\n---\n%s", want, s)
		}
	}
	// pre-prod is disabled → not in webhook_targets.
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "webhook_targets") && strings.Contains(line, "pre-prod") {
			t.Errorf("disabled webhook should not be in webhook_targets: %q", line)
		}
	}

	// prod's image flipped to yes; the [[prod]] subsection still exists.
	reparsed, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse merged output: %v", err)
	}
	prod := reparsed.root.sub("webhooks").sub("prod")
	if prod == nil {
		t.Fatal("prod webhook lost after merge")
	}
	if got := kvVal(prod, "image"); got != "yes" {
		t.Errorf("prod image = %q, want yes", got)
	}
	if got := kvVal(prod, "header"); got != "MAC: 1cf64c54a756" {
		t.Errorf("prod header not preserved: %q", got)
	}
	cam := reparsed.root.sub("cameras").sub("66_3")
	if cam == nil || kvVal(cam, "url") == "" {
		t.Fatal("camera 66_3 url lost after merge")
	}
}

// TestMergeNewWebhookAndCamera covers a device whose config has no matching
// webhook/camera yet — the merge creates them.
func TestMergeNewWebhookAndCamera(t *testing.T) {
	base := "timezone = UTC\n[cameras]\n    regions = us-az\n[webhooks]\n"
	cfg := prconfig.Config{
		CameraID: "0",
		Region:   "us-az",
		Webhooks: []prconfig.Webhook{{Name: "prod", URL: "https://x.com/y", Enabled: true, Image: true}},
	}
	out, err := Merge([]byte(base), cfg, "rtsp://cam/0")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	doc, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cam := doc.root.sub("cameras").sub("0"); cam == nil || kvVal(cam, "url") != "rtsp://cam/0" {
		t.Errorf("new camera not created with url: %s", out)
	}
	if wh := doc.root.sub("webhooks").sub("prod"); wh == nil || kvVal(wh, "url") != "https://x.com/y" {
		t.Errorf("new webhook not created: %s", out)
	}
	if got := kvVal(doc.root.sub("cameras"), "webhook_targets"); got != "prod" {
		t.Errorf("webhook_targets = %q, want prod", got)
	}
}

// TestMergeReordersWebhooks covers a dashboard reorder: webhook ORDER is
// meaningful in PR's config.ini (webhook_targets + the [[name]] subsection
// order), so swapping the cfg.Webhooks order must move BOTH the webhook_targets
// list and the on-disk subsection blocks, while still preserving each webhook's
// unmodeled keys (header, image_quality).
func TestMergeReordersWebhooks(t *testing.T) {
	// realConfig is prod, pre-prod; ask for pre-prod, prod (both enabled).
	cfg := prconfig.Config{
		CameraID: "66_3",
		Region:   "us-az",
		Webhooks: []prconfig.Webhook{
			{Name: "pre-prod", URL: "https://preprod-flask.uknomi.com/recognize_vehicle_event", Enabled: true},
			{Name: "prod", URL: "https://api-flask.uknomi.com/recognize_vehicle_event", Enabled: true},
		},
	}
	out, err := Merge([]byte(realConfig), cfg, "rtsp://x")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	s := string(out)

	// webhook_targets follows the new order.
	if !strings.Contains(s, "webhook_targets = pre-prod, prod") {
		t.Errorf("webhook_targets not reordered:\n%s", s)
	}
	// The [[pre-prod]] subsection block now precedes [[prod]].
	pre := strings.Index(s, "[[pre-prod]]")
	prod := strings.Index(s, "[[prod]]")
	if pre < 0 || prod < 0 || pre > prod {
		t.Errorf("subsection blocks not reordered (pre-prod at %d, prod at %d):\n%s", pre, prod, s)
	}
	// Unmodeled per-webhook keys still preserved through the reorder.
	if strings.Count(s, "header = MAC: 1cf64c54a756") != 2 {
		t.Errorf("webhook headers lost in reorder:\n%s", s)
	}
	// Round-trip: Extract sees the new order.
	got, err := Extract(out)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Webhooks) != 2 || got.Webhooks[0].Name != "pre-prod" || got.Webhooks[1].Name != "prod" {
		t.Errorf("Extract did not reflect reordered webhooks: %+v", got.Webhooks)
	}
}

func TestExtract(t *testing.T) {
	cfg, err := Extract([]byte(realConfig))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if cfg.Region != "us-az" {
		t.Errorf("region = %q, want us-az", cfg.Region)
	}
	if cfg.CameraID != "66_3" {
		t.Errorf("camera_id = %q, want 66_3", cfg.CameraID)
	}
	if len(cfg.Webhooks) != 2 {
		t.Fatalf("webhooks = %d, want 2", len(cfg.Webhooks))
	}
	for _, wh := range cfg.Webhooks {
		if !wh.Enabled { // both are in webhook_targets
			t.Errorf("%s should be enabled (in webhook_targets)", wh.Name)
		}
		if wh.Image { // both have image = no
			t.Errorf("%s image should be false", wh.Name)
		}
		if wh.URL == "" {
			t.Errorf("%s url not extracted", wh.Name)
		}
	}
	// Round-trip: Extract then Merge back should keep webhook_targets intact.
	merged, err := Merge([]byte(realConfig), cfg, "rtsp://x")
	if err != nil {
		t.Fatalf("Merge after Extract: %v", err)
	}
	if !strings.Contains(string(merged), "webhook_targets = prod, pre-prod") {
		t.Errorf("round-trip lost webhook_targets:\n%s", merged)
	}
}

func kvVal(s *Section, key string) string {
	for _, it := range s.items {
		if it.kv != nil && it.kv.key == key {
			return it.kv.value
		}
	}
	return ""
}
