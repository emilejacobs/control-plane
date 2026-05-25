package ingest

import (
	"context"
	"log/slog"
	"time"
)

// LogTailSweeperWriter is the registry slice the log-tail sweeper
// needs. *registry.Registry satisfies it via DeleteStaleLogTails
// added in slice 3 cycle 1.
type LogTailSweeperWriter interface {
	DeleteStaleLogTails(ctx context.Context, olderThan time.Time) (int, error)
}

// LogTailSweeper periodically removes device_log_tails rows older
// than the staleness threshold (default 24h). Per the PRD: long
// enough that a slow operator can re-poll a tab they left open
// overnight, short enough not to bloat the table.
type LogTailSweeper struct {
	writer    LogTailSweeperWriter
	log       *slog.Logger
	interval  time.Duration
	threshold time.Duration
	now       func() time.Time
}

// LogTailSweeperConfig tunes a LogTailSweeper. All fields default.
type LogTailSweeperConfig struct {
	Interval  time.Duration // tick interval; default 1h
	Threshold time.Duration // rows older than threshold get deleted; default 24h
	Logger    *slog.Logger
	Now       func() time.Time
}

func NewLogTailSweeper(w LogTailSweeperWriter, cfg LogTailSweeperConfig) *LogTailSweeper {
	interval := cfg.Interval
	if interval == 0 {
		interval = time.Hour
	}
	threshold := cfg.Threshold
	if threshold == 0 {
		threshold = 24 * time.Hour
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &LogTailSweeper{writer: w, log: log, interval: interval, threshold: threshold, now: now}
}

// Run sweeps on every interval tick until ctx is cancelled.
func (s *LogTailSweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("log tail sweeper stopped")
			return
		case <-ticker.C:
			s.sweepOnce(ctx)
		}
	}
}

func (s *LogTailSweeper) sweepOnce(ctx context.Context) {
	cutoff := s.now().Add(-s.threshold)
	n, err := s.writer.DeleteStaleLogTails(ctx, cutoff)
	if err != nil {
		s.log.Error("log tail sweeper: delete failed", "err", err)
		return
	}
	s.log.Info("log-tail-sweeper.tick", "deleted", n, "cutoff", cutoff.UTC().Format(time.RFC3339))
}
