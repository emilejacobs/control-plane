// Package camerasnapshot implements the agent-side handler for the downward
// camera.snapshot command (issue #8, ADR-030 § 7). CP embeds a CP-minted S3 key
// and a presigned PUT URL in the command; the handler captures one JPEG frame
// from the named camera's RTSP stream, PUTs it straight to S3, and ACKs the key
// + size so cp-ingest can index a device_captures row.
//
// Wire types live in internal/protocol/camerasnapshot so the agent and CP
// halves can't drift. The three side-effects (read cameras, capture frame, PUT)
// are injected so the handler is unit-testable without a camera or network.
package camerasnapshot

import (
	"context"
	"encoding/json"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	protosnapshot "github.com/emilejacobs/control-plane/internal/protocol/camerasnapshot"
)

// CamerasReader returns the device's current camera inventory (the agent's
// local cameras file written by cameras.update).
type CamerasReader interface {
	Cameras(ctx context.Context) ([]cameras.Camera, error)
}

// Snapshotter captures a single still frame from an RTSP URL and returns the
// encoded JPEG bytes. Production shells out to ffmpeg; tests return canned bytes.
type Snapshotter interface {
	Snapshot(ctx context.Context, rtspURL string) ([]byte, error)
}

// Uploader PUTs body to a presigned URL with the given content type.
type Uploader interface {
	Put(ctx context.Context, url, contentType string, body []byte) error
}

type Handler struct {
	cameras   CamerasReader
	snapshots Snapshotter
	uploader  Uploader
}

func New(c CamerasReader, s Snapshotter, u Uploader) *Handler {
	return &Handler{cameras: c, snapshots: s, uploader: u}
}

// Handle runs the snapshot→PUT flow and returns the Result the dispatcher wraps
// into the cmd-result ACK. Each failure maps to a stable coded error so the
// dashboard can render why a refresh failed.
func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	a, err := protosnapshot.ParseArgs(args)
	if err != nil {
		return nil, envelope.NewCodedError(protosnapshot.CodeBadPayload, err.Error())
	}

	list, err := h.cameras.Cameras(ctx)
	if err != nil {
		return nil, envelope.NewCodedError(protosnapshot.CodeCamerasReadFail, err.Error())
	}
	var rtsp string
	found := false
	for _, c := range list {
		if c.CameraID == a.CameraID {
			rtsp = c.RtspURL
			found = true
			break
		}
	}
	if !found {
		return nil, envelope.NewCodedError(protosnapshot.CodeUnknownCamera, "no camera "+a.CameraID+" on this device")
	}

	frame, err := h.snapshots.Snapshot(ctx, rtsp)
	if err != nil {
		return nil, envelope.NewCodedError(protosnapshot.CodeSnapshotFailed, err.Error())
	}

	if err := h.uploader.Put(ctx, a.PutURL, protosnapshot.ContentType, frame); err != nil {
		return nil, envelope.NewCodedError(protosnapshot.CodeUploadFailed, err.Error())
	}

	return protosnapshot.Result{
		S3Key:     a.S3Key,
		SizeBytes: int64(len(frame)),
		CameraID:  a.CameraID,
	}, nil
}
