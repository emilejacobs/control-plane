package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
)

// defaultStaleThreshold is how far past last contact a still-"online" device
// must be before the DB backstop flips it offline. Generous (10 missed 30s
// heartbeats) so it never races a device that just reconnected but hasn't
// heartbeated yet — the in-memory sweep + IoT fast-path handle quick detection;
// this only mops up orphans the in-memory model never knew about.
const defaultStaleThreshold = 5 * time.Minute

// SweepWriter persists the offline transitions the sweeper finds — from the
// in-memory model (SetPresence) and from the DB-backed staleness backstop
// (ReconcileStalePresence). *registry.Registry satisfies it.
type SweepWriter interface {
	SetPresence(ctx context.Context, deviceID string, online bool, at time.Time) error
	ReconcileStalePresence(ctx context.Context, staleBefore, now time.Time) (int, error)
}

// PresenceSweeper periodically asks the Presence model which devices have
// gone stale and persists each offline transition. It is the backstop for
// the lifecycle fast-path (ADR-018, sweeper as a goroutine): a device that
// dies without a clean disconnect is caught here within one tick of
// crossing the freshness threshold.
//
// The in-memory model is ephemeral, so each tick also runs a DB-backed
// reconcile that catches devices the model never learned about (e.g. one that
// died before a cp-ingest restart) — without it, such a device stays
// is_online=true forever while last_seen goes stale.
type PresenceSweeper struct {
	presence   *presence.Presence
	writer     SweepWriter
	log        *slog.Logger
	interval   time.Duration
	staleAfter time.Duration
	now        func() time.Time
}

// SweeperConfig tunes a PresenceSweeper. All fields default.
type SweeperConfig struct {
	Interval       time.Duration // tick interval; default 30s
	StaleThreshold time.Duration // DB-backstop staleness cutoff; default 5m
	Logger         *slog.Logger
	Now            func() time.Time
}

func NewPresenceSweeper(p *presence.Presence, w SweepWriter, cfg SweeperConfig) *PresenceSweeper {
	interval := cfg.Interval
	if interval == 0 {
		interval = 30 * time.Second
	}
	staleAfter := cfg.StaleThreshold
	if staleAfter == 0 {
		staleAfter = defaultStaleThreshold
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &PresenceSweeper{presence: p, writer: w, log: log, interval: interval, staleAfter: staleAfter, now: now}
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

	// DB-backed backstop: flip any device still marked online whose last
	// contact is older than the staleness cutoff — catching orphans the
	// in-memory model above never saw (died before a cp-ingest restart, no
	// IoT disconnect). Idempotent: an already-offline device is excluded.
	reconciled, err := s.writer.ReconcileStalePresence(ctx, now.Add(-s.staleAfter), now)
	if err != nil {
		s.log.Error("failed to reconcile stale presence", "err", err)
	} else if reconciled > 0 {
		s.log.Info("audit.presence", "event", "reconcile_offline", "count", reconciled, "online", false)
	}

	s.log.Info("sweeper.tick", "transitions", transitions, "reconciled", reconciled)
}
