package heartbeat_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/uknomi/control-plane/internal/handlers/heartbeat"
)

func TestHeartbeatReturnsExpectedFields(t *testing.T) {
	startTime := time.Now().Add(-30 * time.Second)
	h := heartbeat.New("dev-001", "0.1.0", startTime)

	result, err := h.Handle(context.Background(), nil)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	payload, ok := result.(heartbeat.Response)
	if !ok {
		t.Fatalf("expected heartbeat.Response, got %T", result)
	}

	if payload.DeviceID != "dev-001" {
		t.Errorf("DeviceID: got %q, want dev-001", payload.DeviceID)
	}
	if payload.Version != "0.1.0" {
		t.Errorf("Version: got %q, want 0.1.0", payload.Version)
	}
	if payload.OS != runtime.GOOS {
		t.Errorf("OS: got %q, want %q", payload.OS, runtime.GOOS)
	}
	if payload.UptimeSeconds < 29 || payload.UptimeSeconds > 31 {
		t.Errorf("UptimeSeconds: got %d, want ~30", payload.UptimeSeconds)
	}
}
