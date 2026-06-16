// Package snapshotscheduler runs the agent's scheduled-snapshot loop (issue #9):
// on the device's cadence it captures a frame from each camera and uploads it
// via the generic captures handshake. The cadence + per-camera next-fire times
// live in the snapshot state file, so a restart resumes the schedule rather than
// resetting it (the issue's persistence AC). Unlike the on-demand
// camera.snapshot handler, the scheduler runs in its own goroutine, so it can
// use the request/grant uploader without deadlocking the command router.
package snapshotscheduler

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/snapshotstate"
)

// CamerasReader returns the device's current camera inventory.
type CamerasReader interface {
	Cameras(ctx context.Context) ([]cameras.Camera, error)
}

// Snapshotter captures a single JPEG frame from an RTSP URL.
type Snapshotter interface {
	Snapshot(ctx context.Context, rtspURL string) ([]byte, error)
}

// Uploader uploads bytes via the generic captures handshake and returns the key.
// *captureupload.Uploader satisfies it.
type Uploader interface {
	Upload(ctx context.Context, kind, contentType string, data []byte, metadata map[string]any) (string, error)
}

// StateStore reads the persisted cadence/schedule and records next-fire times.
// *snapshotstate.Store satisfies it.
type StateStore interface {
	Load() (snapshotstate.State, error)
	SetNextFire(cameraID string, at time.Time) error
}

const snapshotContentType = "image/jpeg"

// CadenceInterval maps a cadence to its period. "off"/unknown → 0 (disabled).
func CadenceInterval(cadence string) time.Duration {
	switch cadence {
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

type Scheduler struct {
	cameras   CamerasReader
	snapshots Snapshotter
	uploader  Uploader
	state     StateStore
	now       func() time.Time
	interval  time.Duration
	logger    *slog.Logger
}

type Option func(*Scheduler)

// WithNow overrides the clock (tests).
func WithNow(f func() time.Time) Option { return func(s *Scheduler) { s.now = f } }

// WithCheckInterval sets how often the loop evaluates due cameras (default 1h —
// fine for daily/weekly cadences). Tests pin it small.
func WithCheckInterval(d time.Duration) Option { return func(s *Scheduler) { s.interval = d } }

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(s *Scheduler) { s.logger = l } }

func New(c CamerasReader, snap Snapshotter, up Uploader, state StateStore, opts ...Option) *Scheduler {
	s := &Scheduler{
		cameras:   c,
		snapshots: snap,
		uploader:  up,
		state:     state,
		now:       time.Now,
		logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	s.interval = time.Hour
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run evaluates due cameras every check interval until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.CheckAndFire(ctx)
		}
	}
}

// CheckAndFire captures + uploads a snapshot for every camera whose next-fire
// time has passed (or which has never fired), then reschedules it. Exported so
// the loop body is unit-testable without driving the ticker.
func (s *Scheduler) CheckAndFire(ctx context.Context) {
	st, err := s.state.Load()
	if err != nil {
		s.logger.Error("snapshot scheduler: load state failed", "error", err)
		return
	}
	interval := CadenceInterval(st.Cadence)
	if interval == 0 {
		return // cadence off or unset — nothing scheduled
	}

	cams, err := s.cameras.Cameras(ctx)
	if err != nil {
		s.logger.Error("snapshot scheduler: read cameras failed", "error", err)
		return
	}

	now := s.now()
	for _, cam := range cams {
		next, scheduled := st.NextFire[cam.CameraID]
		// Fire if the camera has never been scheduled (first sight / cadence
		// just enabled — gives a prompt first snapshot) or its time has passed.
		if scheduled && now.Before(next) {
			continue
		}
		s.fire(ctx, cam)
		// Reschedule regardless of success so a broken camera doesn't retry
		// every tick; a failed capture is logged and waits for the next cycle.
		if err := s.state.SetNextFire(cam.CameraID, now.Add(interval)); err != nil {
			s.logger.Error("snapshot scheduler: persist next-fire failed", "camera_id", cam.CameraID, "error", err)
		}
	}
}

func (s *Scheduler) fire(ctx context.Context, cam cameras.Camera) {
	frame, err := s.snapshots.Snapshot(ctx, cam.RtspURL)
	if err != nil {
		s.logger.Error("snapshot scheduler: capture failed", "camera_id", cam.CameraID, "error", err)
		return
	}
	key, err := s.uploader.Upload(ctx, "snapshot", snapshotContentType, frame, map[string]any{"camera_id": cam.CameraID})
	if err != nil {
		s.logger.Error("snapshot scheduler: upload failed", "camera_id", cam.CameraID, "error", err)
		return
	}
	s.logger.Info("scheduled snapshot uploaded", "camera_id", cam.CameraID, "s3_key", key)
}
