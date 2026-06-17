package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/camerastatus"
)

// CameraStatusWriter is the persistence side of the camera-status
// ingester — satisfied by *registry.Registry (#112's UpdateCameraStatus).
type CameraStatusWriter interface {
	UpdateCameraStatus(ctx context.Context, deviceID, cameraID, status string, checkedAt time.Time) error
}

// CameraStatusIngester records an agent's per-camera reachability report
// (#113) onto the device_cameras rows. It mirrors ServiceStatusIngester:
// the IoT Rule → SQS hop delivers a camerastatus.Report and Handle
// upserts each camera's status.
type CameraStatusIngester struct {
	writer CameraStatusWriter
	now    func() time.Time
	// Logger receives one line per camera the CP doesn't know about
	// (skipped, not poisoned). Nil → discard.
	Logger *slog.Logger
}

// NewCameraStatusIngester wires the writer; now defaults to time.Now.
func NewCameraStatusIngester(w CameraStatusWriter, now func() time.Time) *CameraStatusIngester {
	if now == nil {
		now = time.Now
	}
	return &CameraStatusIngester{writer: w, now: now}
}

// Handle applies one camera-status report. The cp-side wall clock is the
// authoritative last-checked timestamp — agent clocks drift, so
// r.ReportedAt is informational only.
//
// Error policy mirrors the other ingesters but at camera granularity:
//   - empty device_id → Poison (can never succeed; straight to DLQ).
//   - a camera the CP doesn't know (ErrCameraNotFound / ErrDeviceNotFound:
//     removed in CP, decommissioned device, or probed before the import) →
//     skipped and logged, NOT poison, so the report's other cameras still
//     apply and the message isn't retried forever.
//   - any other writer error → returned unchanged so SQS redelivers.
func (i *CameraStatusIngester) Handle(ctx context.Context, r camerastatus.Report) error {
	if r.DeviceID == "" {
		return sqsconsumer.Poison(errors.New("camera-status report has no device_id"))
	}
	log := i.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	checkedAt := i.now()
	for _, c := range r.Cameras {
		if c.CameraID == "" {
			continue // malformed entry — nothing to key on
		}
		if err := i.writer.UpdateCameraStatus(ctx, r.DeviceID, c.CameraID, c.Status, checkedAt); err != nil {
			if errors.Is(err, registry.ErrCameraNotFound) || errors.Is(err, registry.ErrDeviceNotFound) {
				log.Info("camera-status.unknown_camera",
					"device_id", r.DeviceID,
					"camera_id", c.CameraID,
					"correlation_id", r.CorrelationID,
				)
				continue
			}
			return err
		}
	}
	return nil
}
