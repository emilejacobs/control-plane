package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// HealthProbeWriter persists a fleet-health-probe report. The writer
// owns the per-probe UPSERT semantics; the ingester just hands over the
// device_id + per-probe rows + the cp-side ingest timestamp.
type HealthProbeWriter interface {
	RecordHealthProbes(ctx context.Context, deviceID string, results []healthprobes.Result, observedAt time.Time) error
}

// HealthProbeIngester is the SQSConsumer handler for the health-probes
// queue (#19). It persists every report; an empty device_id or an
// unknown device is poison (DLQ-bound, no retry); transient writer
// errors propagate so SQS redelivers. Mirrors ServiceStatusIngester.
type HealthProbeIngester struct {
	writer HealthProbeWriter
	now    func() time.Time
	// Logger receives one "health-probe.red" line per red probe — the
	// signal the per-probe-type CloudWatch alarm's metric filter counts.
	// Emitting here keeps the alarm signal next to the protocol fact.
	Logger *slog.Logger
}

// NewHealthProbeIngester builds the ingester. now defaults to time.Now.
func NewHealthProbeIngester(w HealthProbeWriter, now func() time.Time) *HealthProbeIngester {
	if now == nil {
		now = time.Now
	}
	return &HealthProbeIngester{writer: w, now: now}
}

// Handle is the sqsconsumer.Handler[healthprobes.Report].
func (i *HealthProbeIngester) Handle(ctx context.Context, r healthprobes.Report) error {
	if r.DeviceID == "" {
		return sqsconsumer.Poison(errors.New("health-probes report has no device_id"))
	}
	// cp-side wall clock is authoritative; agent clocks drift, so
	// r.ReportedAt is informational only.
	if err := i.writer.RecordHealthProbes(ctx, r.DeviceID, r.Probes, i.now()); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}

	log := i.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	for _, p := range r.Probes {
		if p.Status == healthprobes.StatusRed {
			log.Info("health-probe.red",
				"device_id", r.DeviceID,
				"probe", p.Name,
				"state", p.State,
				"correlation_id", r.CorrelationID,
			)
		}
	}
	return nil
}

// Compile-time checks that the health-probes plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated                   = healthprobes.Report{}
	_ sqsconsumer.Handler[healthprobes.Report] = (*HealthProbeIngester)(nil).Handle
)
