package ingest

import (
	"context"
	"log/slog"
	"time"
)

// DeviceServicesSweeperWriter is the registry slice the sweeper
// needs. *registry.Registry satisfies it via DeleteStaleDeviceServices.
type DeviceServicesSweeperWriter interface {
	DeleteStaleDeviceServices(ctx context.Context, olderThan time.Time) (int, error)
}

// DeviceServicesSweeper periodically removes device_services rows
// whose last_reported is older than the staleness threshold (default
// 15 minutes = 3× the default 5-minute service-status cadence — one
// missed report is fine, two in a row drops the row).
//
// Without this, an operator who removes a service from a device's
// allow-list via the EditServicesModal sees the dead row linger
// indefinitely in the Services panel (RecordServiceStates does per-
// service UPSERT, not replace-all-per-device, by design — slice 1).
type DeviceServicesSweeper struct {
	writer    DeviceServicesSweeperWriter
	log       *slog.Logger
	interval  time.Duration
	threshold time.Duration
	now       func() time.Time
}

// DeviceServicesSweeperConfig tunes a DeviceServicesSweeper. All
// fields default.
type DeviceServicesSweeperConfig struct {
	Interval  time.Duration // tick interval; default 10 min
	Threshold time.Duration // rows older than threshold get deleted; default 15 min
	Logger    *slog.Logger
	Now       func() time.Time
}

func NewDeviceServicesSweeper(w DeviceServicesSweeperWriter, cfg DeviceServicesSweeperConfig) *DeviceServicesSweeper {
	interval := cfg.Interval
	if interval == 0 {
		interval = 10 * time.Minute
	}
	threshold := cfg.Threshold
	if threshold == 0 {
		threshold = 15 * time.Minute
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &DeviceServicesSweeper{writer: w, log: log, interval: interval, threshold: threshold, now: now}
}

// Run sweeps on every interval tick until ctx is cancelled.
func (s *DeviceServicesSweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("device services sweeper stopped")
			return
		case <-ticker.C:
			s.sweepOnce(ctx)
		}
	}
}

func (s *DeviceServicesSweeper) sweepOnce(ctx context.Context) {
	cutoff := s.now().Add(-s.threshold)
	n, err := s.writer.DeleteStaleDeviceServices(ctx, cutoff)
	if err != nil {
		s.log.Error("device services sweeper: delete failed", "err", err)
		return
	}
	s.log.Info("device-services-sweeper.tick", "deleted", n, "cutoff", cutoff.UTC().Format(time.RFC3339))
}
