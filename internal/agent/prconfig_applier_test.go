package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

func TestPRConfigApplierMergesAndRestarts(t *testing.T) {
	const existing = "timezone = UTC\n[cameras]\n    regions = us-az\n    [[66_3]]\n        url = rtsp://old\n        active = yes\n[webhooks]\n    [[prod]]\n        url = https://x/y\n        name = prod\n        header = MAC: abc\n        image = no\n"
	var wrote []byte
	restarted := false
	a := &prConfigApplier{
		configPath: "/x/config.ini",
		readFile:   func(string) ([]byte, error) { return []byte(existing), nil },
		writeFile:  func(_ string, b []byte, _ os.FileMode) error { wrote = b; return nil },
		restart:    func(context.Context) error { restarted = true; return nil },
	}
	req := prconfig.UpdateRequest{
		Config: prconfig.Config{
			CameraID: "66_3",
			Region:   "us-ca",
			Webhooks: []prconfig.Webhook{{Name: "prod", URL: "https://x/y", Enabled: true, Image: true}},
		},
		LPRCameraRtspURL: "rtsp://new",
	}
	if err := a.Apply(context.Background(), req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !restarted {
		t.Error("container not restarted")
	}
	s := string(wrote)
	for _, want := range []string{"regions = us-ca", "url = rtsp://new", "header = MAC: abc", "image = yes", "webhook_targets = prod"} {
		if !strings.Contains(s, want) {
			t.Errorf("merged config missing %q\n%s", want, s)
		}
	}
}

func TestPRConfigApplierNoRunner(t *testing.T) {
	a := &prConfigApplier{
		configPath: "/x/config.ini",
		readFile:   func(string) ([]byte, error) { return []byte("[cameras]\n"), nil },
		writeFile:  func(string, []byte, os.FileMode) error { return nil },
		restart:    nil, // no auto-login user
	}
	err := a.Apply(context.Background(), prconfig.UpdateRequest{Config: prconfig.Config{CameraID: "0", Region: "us-az"}})
	if err == nil || !strings.Contains(err.Error(), "ALPR unavailable") {
		t.Errorf("expected ALPR-unavailable error, got %v", err)
	}
}

func TestPRConfigApplierReadError(t *testing.T) {
	a := &prConfigApplier{
		configPath: "/x/config.ini",
		readFile:   func(string) ([]byte, error) { return nil, errors.New("nope") },
		writeFile:  func(string, []byte, os.FileMode) error { return nil },
	}
	if err := a.Apply(context.Background(), prconfig.UpdateRequest{Config: prconfig.Config{CameraID: "0", Region: "us-az"}}); err == nil {
		t.Error("expected read error")
	}
}
