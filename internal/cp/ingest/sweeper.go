package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
)

// PresenceSweeper periodically asks the Presence model which devices have
// gone stale and persists each offline transition. It is the backstop for
// the lifecycle fast-path (ADR-018, sweeper as a goroutine): a device that
// dies without a clean disconnect is caught here within one tick of
// crossing the freshness threshold.
type PresenceSweeper struct {
	presence *presence.Presence
	writer   PresenceWriter
	log      *slog.Logger
	interval time.Duration
	now      func() time.Time
}

// SweeperConfig tunes a PresenceSweeper. All fields default.
type SweeperConfig struct {
	Interval time.Duration // tick interval; default 30s
	Logger   *slog.Logger
	Now      func() time.Time
}

func NewPresenceSweeper(p *presence.Presence, w PresenceWriter, cfg SweeperConfig) *PresenceSweeper {
	interval := cfg.Interval
	if interval == 0 {
		interval = 30 * time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &PresenceSweeper{presence: p, writer: w, log: log, interval: interval, now: now}
}

// Run sweeps on every interval tick until ctx is cancelled.
func (s *PresenceSweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("presence sweeper stopped")
			return
		case <-ticker.C:
			s.sweepOnce(ctx)
		}
	}
}

// sweepOnce runs one sweep and persists the offline transitions it finds.
// Sweep is idempotent, so a device already offline is not re-persisted.
// Every sweep emits a "sweeper.tick" heartbeat (whether or not there were
// transitions); the Issue 21 lag alarm pages when the count falls to zero.
func (s *PresenceSweeper) sweepOnce(ctx context.Context) {
	now := s.now()
	transitions := 0
	for _, tr := range s.presence.Sweep(now) {
		if err := s.writer.SetPresence(ctx, tr.DeviceID, false, now); err != nil {
			s.log.Error("failed to persist sweep transition",
				"device_id", tr.DeviceID, "err", err)
			continue
		}
		s.log.Info("audit.presence",
			"event", "sweep_offline", "device_id", tr.DeviceID, "online", false)
		transitions++
	}
	s.log.Info("sweeper.tick", "transitions", transitions)
}
