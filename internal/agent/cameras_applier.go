package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// CamerasApplier persists a cameras.update payload to the agent-
// managed local cameras file. Source of truth lives in CP (ADR-029);
// this file is the downstream copy the new Edge UI's live-preview
// route reads from when it streams MJPEG.
//
// Atomic write: marshal → write to a .tmp sibling in the same dir →
// fsync → os.Rename over the target. Crash-resilient: a partial
// write never leaves an unreadable cameras.json for the consumer.
//
// The file shape is `{"cameras": [{camera_id, label, rtsp_url,
// is_lpr}, ...]}` — mirrors the cmd payload exactly so consumers
// can json-unmarshal directly into the wire type.
type CamerasApplier struct {
	path string
}

// NewCamerasApplier returns an Applier writing to path. The parent
// directory must exist (the install module creates it); the Applier
// does not mkdir.
func NewCamerasApplier(path string) *CamerasApplier {
	return &CamerasApplier{path: path}
}

// Apply marshals the list under a `cameras` envelope and atomically
// writes it to the configured path. Returns the input list unchanged
// (the agent does no transforms; effective == input).
func (a *CamerasApplier) Apply(_ context.Context, list []cameras.Camera) ([]cameras.Camera, error) {
	// Coerce nil → empty so the on-disk JSON is `{"cameras": []}`
	// not `{"cameras": null}`. Consumers (live-preview route, future
	// PR config builder) treat the two states identically, but the
	// explicit-empty form is easier for humans inspecting the file.
	if list == nil {
		list = []cameras.Camera{}
	}
	payload := struct {
		Cameras []cameras.Camera `json:"cameras"`
	}{Cameras: list}

	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal cameras: %w", err)
	}
	if err := atomicWriteBytes(a.path, raw); err != nil {
		return nil, fmt.Errorf("write cameras file: %w", err)
	}
	return list, nil
}

// atomicWriteBytes writes data to path atomically by writing to a
// temp file in the same directory and renaming. Preserves the
// existing file's permission bits if it exists; defaults to 0600 for
// new files (matches the conservative posture of agent state files).
func atomicWriteBytes(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
