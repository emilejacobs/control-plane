package edgeui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// fixtureCameras writes the agent-format cameras.json (the same shape
// internal/agent/cameras_applier.go atomically writes) into the given
// path and returns the canonical Camera slice for cross-checks.
func fixtureCameras(t *testing.T, path string, list []cameras.Camera) {
	t.Helper()
	payload := struct {
		Cameras []cameras.Camera `json:"cameras"`
	}{Cameras: list}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func TestReadCameras_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")
	fixtureCameras(t, path, []cameras.Camera{
		{CameraID: "cam1", Label: "Drive-thru", RtspURL: "rtsp://u:p@10.0.0.42/stream", IsLPR: true},
		{CameraID: "cam2", Label: "Entry", RtspURL: "rtsp://u:p@10.0.0.43/stream", IsLPR: false},
	})

	got, err := ReadCameras(path)
	if err != nil {
		t.Fatalf("ReadCameras: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got["cam1"].Label != "Drive-thru" || got["cam1"].RtspURL != "rtsp://u:p@10.0.0.42/stream" || !got["cam1"].IsLPR {
		t.Errorf("cam1 mismatch: %+v", got["cam1"])
	}
	if got["cam2"].IsLPR {
		t.Errorf("cam2 should not be LPR")
	}
}

func TestReadCameras_MissingFile_EmptyMapNilError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")

	got, err := ReadCameras(path)
	if err != nil {
		t.Fatalf("ReadCameras on missing file should return nil error, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ReadCameras on missing file should return empty map, got %d entries", len(got))
	}
}

func TestReadCameras_MalformedJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := ReadCameras(path)
	if err == nil {
		t.Fatalf("expected error for malformed JSON, got nil")
	}
}

func TestReadCameras_UnknownID_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cameras.json")
	fixtureCameras(t, path, []cameras.Camera{
		{CameraID: "cam1", Label: "Drive-thru", RtspURL: "rtsp://x/s", IsLPR: false},
	})

	got, err := ReadCameras(path)
	if err != nil {
		t.Fatalf("ReadCameras: %v", err)
	}
	if _, ok := got["cam99"]; ok {
		t.Fatalf("cam99 should not be present")
	}
}

// Verify the sentinel error shape callers will use to surface 404.
func TestErrCameraNotFound_IsSentinel(t *testing.T) {
	if ErrCameraNotFound == nil {
		t.Fatalf("ErrCameraNotFound must be a non-nil sentinel error")
	}
	// errors.Is should match itself (sanity check on the export).
	if !errors.Is(ErrCameraNotFound, ErrCameraNotFound) {
		t.Fatalf("errors.Is(sentinel, sentinel) returned false")
	}
}
