package ingest

import (
	"context"
	"log/slog"
	"time"
)

// SnapshotSweeperWriter is the registry slice the snapshot sweeper needs.
// *registry.Registry satisfies it.
type SnapshotSweeperWriter interface {
	DeleteSnapshotsOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}

// SnapshotSweeper periodically prunes snapshot capture rows older than the
// retention threshold (#9). The captures bucket's S3 lifecycle expires the
// objects after 90 days; this keeps the device_captures index in step so the
// history view never lists a row whose object is already gone.
type SnapshotSweeper struct {
	writer    SnapshotSweeperWriter
	log       *slog.Logger
	interval  time.Duration
	threshold time.Duration
	now       func() time.Time
}

// SnapshotSweeperConfig tunes a SnapshotSweeper. All fields default.
type SnapshotSweeperConfig struct {
	Interval  time.Duration // tick interval; default 6h
	Threshold time.Duration // rows older than threshold get deleted; default 90 days
	Logger    *slog.Logger
	Now       func() time.Time
}

func NewSnapshotSweeper(w SnapshotSweeperWriter, cfg SnapshotSweeperConfig) *SnapshotSweeper {
	interval := cfg.Interval
	if interval == 0 {
		interval = 6 * time.Hour
	}
	threshold := cfg.Threshold
	if threshold == 0 {
		threshold = 90 * 24 * time.Hour
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &SnapshotSweeper{writer: w, log: log, interval: interval, threshold: threshold, now: now}
}

// Run sweeps on every interval tick until ctx is cancelled.
func (s *SnapshotSweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("snapshot sweeper stopped")
			return
		case <-ticker.C:
			s.SweepOnce(ctx)
		}
	}
}

// SweepOnce deletes snapshot rows past the retention horizon. Exported so the
// sweep body is unit-testable without driving the ticker.
func (s *SnapshotSweeper) SweepOnce(ctx context.Context) {
	cutoff := s.now().Add(-s.threshold)
	n, err := s.writer.DeleteSnapshotsOlderThan(ctx, cutoff)
	if err != nil {
		s.log.Error("snapshot sweeper: delete failed", "err", err)
		return
	}
	s.log.Info("snapshot-sweeper.tick", "deleted", n, "cutoff", cutoff.UTC().Format(time.RFC3339))
}
