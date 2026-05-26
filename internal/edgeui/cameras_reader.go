// Package edgeui holds the device-local Edge UI's Go pieces: the
// cameras file reader, the Tailscale listener detection, and the MJPEG
// preview handler. The Next.js front-end lives next door at edge-ui/
// and is embedded by cmd/uknomi-edge-ui/main.go.
//
// CP owns the cameras inventory (ADR-029, ADR-030 § 1). The agent's
// cameras.update handler writes a downstream copy at the path in
// cfg.CamerasPath; this package reads that file. The two halves
// pin to the same wire type (internal/protocol/cameras.Camera) so
// they can't drift on field shape.
package edgeui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// ErrCameraNotFound is the sentinel returned (or wrapped) when a
// /preview/<camera_id> request resolves against a missing camera_id.
// The preview handler maps it to a 404 + JSON body.
var ErrCameraNotFound = errors.New("camera not found")

// camerasFile mirrors the on-disk shape internal/agent.CamerasApplier
// writes — a top-level `cameras` key, nothing else.
type camerasFile struct {
	Cameras []cameras.Camera `json:"cameras"`
}

// ReadCameras reads the agent-written cameras.json at path and returns
// a map keyed by camera_id. A missing file is not an error: it returns
// an empty map (the device has not yet received its first
// cameras.update). Malformed JSON or unreadable files are returned as
// errors verbatim — the caller surfaces them through the same 404
// path as a missing camera_id, since the user-facing failure is the
// same.
func ReadCameras(path string) (map[string]cameras.Camera, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]cameras.Camera{}, nil
		}
		return nil, fmt.Errorf("read cameras file %s: %w", path, err)
	}
	var f camerasFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse cameras file %s: %w", path, err)
	}
	out := make(map[string]cameras.Camera, len(f.Cameras))
	for _, c := range f.Cameras {
		out[c.CameraID] = c
	}
	return out, nil
}
