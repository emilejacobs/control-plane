// Package camerasnapshot holds the wire types for the camera.snapshot command
// (issue #8, ADR-030 § 7). camera.snapshot is CP-initiated: CP mints the S3 key
// and presigns the PUT URL up front and embeds both in the command, so the
// agent uploads directly without the upload.request/upload.url round-trip. That
// round-trip would deadlock the agent's (ordered) command router, since the
// snapshot handler would be blocking it while waiting for the reply. The
// agent-initiated generic flow (internal/protocol/upload) serves producers that
// run outside a command handler (e.g. the Slice C audio fsnotify watcher).
//
// Shared by cp-api (builds Args), the agent handler (consumes Args, returns
// Result), and cp-ingest (indexes from Result).
package camerasnapshot

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ContentType is the snapshot's fixed media type; the agent captures a single
// JPEG frame and CP presigns the PUT for exactly this type.
const ContentType = "image/jpeg"

// Error codes carried back in the cmd-result envelope on the failure path.
const (
	CodeBadPayload      = "camera_snapshot.bad_payload"
	CodeUnknownCamera   = "camera_snapshot.unknown_camera"
	CodeSnapshotFailed  = "camera_snapshot.snapshot_failed"
	CodeUploadFailed    = "camera_snapshot.upload_failed"
	CodeCamerasReadFail = "camera_snapshot.cameras_unavailable"
)

// Args is the camera.snapshot command payload (CP → agent). S3Key + PutURL are
// CP-minted; the agent does not choose where the bytes land.
type Args struct {
	CameraID string `json:"camera_id"`
	S3Key    string `json:"s3_key"`
	PutURL   string `json:"put_url"`
}

// ParseArgs decodes + validates a camera.snapshot command payload, rejecting
// unknown fields so a malformed push fails loudly rather than silently.
func ParseArgs(raw json.RawMessage) (Args, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a Args
	if err := dec.Decode(&a); err != nil {
		return Args{}, fmt.Errorf("decode camera.snapshot args: %w", err)
	}
	if a.CameraID == "" {
		return Args{}, fmt.Errorf("camera.snapshot: empty camera_id")
	}
	if a.S3Key == "" {
		return Args{}, fmt.Errorf("camera.snapshot: empty s3_key")
	}
	if a.PutURL == "" {
		return Args{}, fmt.Errorf("camera.snapshot: empty put_url")
	}
	return a, nil
}

// Result is the agent's ACK payload (agent → CP). cp-ingest indexes a
// device_captures row from it (kind=snapshot, content_type=image/jpeg).
type Result struct {
	S3Key     string `json:"s3_key"`
	SizeBytes int64  `json:"size_bytes"`
	CameraID  string `json:"camera_id"`
}
