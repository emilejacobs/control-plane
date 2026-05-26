package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// TestMain_StartServer_HealthAndRoot stands up the Edge UI server on a
// real loopback port, hits /healthz and /, and asserts both return
// reasonable shapes. Skips the MJPEG path — preview is covered by
// internal/edgeui/preview_test.go.
func TestMain_StartServer_HealthAndRoot(t *testing.T) {
	dir := t.TempDir()
	camerasPath := filepath.Join(dir, "cameras.json")
	payload := struct {
		Cameras []cameras.Camera `json:"cameras"`
	}{Cameras: []cameras.Camera{{
		CameraID: "cam1",
		Label:    "Drive-thru",
		RtspURL:  "rtsp://host/stream",
	}}}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(camerasPath, raw, 0o600); err != nil {
		t.Fatalf("write cameras: %v", err)
	}

	srv, addr, err := startServer(serverConfig{
		ListenAddr:  "127.0.0.1:0",
		CamerasPath: camerasPath,
	})
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	defer srv.Close()

	base := "http://" + addr
	// /healthz
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("healthz Get: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("healthz status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "ok") {
		t.Errorf("healthz body: %s", body)
	}

	// / (root) should return index.html.
	resp2, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("root Get: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("root status: %d", resp2.StatusCode)
	}
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "uKnomi Edge") {
		t.Errorf("root body did not contain uKnomi Edge: %s", body2[:min(len(body2), 200)])
	}
}

// TestMain_StartServer_ContextShutdown ensures the server can be
// closed cleanly via Shutdown(ctx) so launchd's SIGTERM doesn't
// produce a noisy log line.
func TestMain_StartServer_ContextShutdown(t *testing.T) {
	dir := t.TempDir()
	camerasPath := filepath.Join(dir, "cameras.json")
	_ = os.WriteFile(camerasPath, []byte(`{"cameras":[]}`), 0o600)

	srv, _, err := startServer(serverConfig{
		ListenAddr:  "127.0.0.1:0",
		CamerasPath: camerasPath,
	})
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Close did not return")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
