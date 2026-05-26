package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// Apply round-trips a non-empty list through the JSON encoder; the
// file on disk has the exact wire shape (consumers can unmarshal
// directly into protocol/cameras.UpdateAllRequest).
func TestCamerasApplierApplyWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")
	a := agent.NewCamerasApplier(path)

	in := []cameras.Camera{
		{CameraID: "cam1", Label: "Drive-thru", RtspURL: "rtsp://a/stream", IsLPR: true},
		{CameraID: "cam2", Label: "Entry", RtspURL: "rtsp://b/stream", IsLPR: false},
	}
	out, err := a.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(out) != 2 || out[0].CameraID != "cam1" {
		t.Errorf("returned list: got %+v", out)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	var got cameras.UpdateAllRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("file is not valid cameras-update JSON: %v", err)
	}
	if len(got.Cameras) != 2 || got.Cameras[0].CameraID != "cam1" || !got.Cameras[0].IsLPR {
		t.Errorf("file contents: got %+v", got)
	}
}

// Empty list on disk is `{"cameras": []}` not `{"cameras": null}`.
// Consumers parse both the same way, but the explicit-empty form is
// friendlier for humans inspecting the file directly.
func TestCamerasApplierEmptyListWritesEmptyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")
	a := agent.NewCamerasApplier(path)

	if _, err := a.Apply(context.Background(), nil); err != nil {
		t.Fatalf("Apply(nil): %v", err)
	}

	raw, _ := os.ReadFile(path)
	var probe struct {
		Cameras any `json:"cameras"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	arr, ok := probe.Cameras.([]any)
	if !ok {
		t.Fatalf("cameras field type: got %T want []any (i.e., JSON array)", probe.Cameras)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array, got %d entries", len(arr))
	}
}

// Atomic write: a second Apply replaces the file content fully,
// without intermediate states leaving stale data.
func TestCamerasApplierApplyReplacesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")
	a := agent.NewCamerasApplier(path)

	first := []cameras.Camera{{CameraID: "cam1", Label: "old", RtspURL: "rtsp://old", IsLPR: false}}
	if _, err := a.Apply(context.Background(), first); err != nil {
		t.Fatalf("Apply #1: %v", err)
	}

	second := []cameras.Camera{{CameraID: "cam1", Label: "new", RtspURL: "rtsp://new", IsLPR: true}}
	if _, err := a.Apply(context.Background(), second); err != nil {
		t.Fatalf("Apply #2: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var got cameras.UpdateAllRequest
	_ = json.Unmarshal(raw, &got)
	if got.Cameras[0].Label != "new" || !got.Cameras[0].IsLPR {
		t.Errorf("expected updated content, got %+v", got.Cameras[0])
	}

	// No leftover .tmp files in the dir (cleanup happens on success).
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("stale temp file left behind: %s", e.Name())
		}
	}
}

// File mode 0600 on a fresh write — agent state files default to
// owner-only.
func TestCamerasApplierFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")
	a := agent.NewCamerasApplier(path)

	if _, err := a.Apply(context.Background(), []cameras.Camera{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode: got %o want 0600", got)
	}
}
