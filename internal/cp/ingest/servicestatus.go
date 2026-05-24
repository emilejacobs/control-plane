package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
	"github.com/emilejacobs/control-plane/internal/service"
)

// ServiceStatusWriter persists a service-status report. The writer
// owns the per-service UPSERT semantics; the ingester just hands over
// the device_id + per-service rows + the cp-side ingest timestamp.
// *registry.Registry will satisfy it once the storage migration lands.
type ServiceStatusWriter interface {
	RecordServiceStates(ctx context.Context, deviceID string, states []servicestatus.ServiceState, reportedAt time.Time) error
}

// ServiceStatusIngester is the SQSConsumer handler for the
// service-status queue. It persists every report; the writer is
// responsible for the per-service UPSERT and the last_reported stamp.
//
// Per the heartbeat pattern: an empty device_id or an unknown device
// is poison (DLQ-bound, no retry); transient writer errors propagate
// so SQS redelivers.
type ServiceStatusIngester struct {
	writer ServiceStatusWriter
	now    func() time.Time
	// Logger receives one structured info line per service in the
	// report. The "service-status.stopped" message is what the Phase 2
	// CloudWatch alarm's metric filter counts; emitting it here keeps
	// the alarm-firing signal next to the data that fires it. Nil
	// defaults to a discard logger so tests stay quiet.
	Logger *slog.Logger
}

// NewServiceStatusIngester builds the ingester. now defaults to time.Now.
func NewServiceStatusIngester(w ServiceStatusWriter, now func() time.Time) *ServiceStatusIngester {
	if now == nil {
		now = time.Now
	}
	return &ServiceStatusIngester{writer: w, now: now}
}

// Handle is the sqsconsumer.Handler[servicestatus.Report].
func (i *ServiceStatusIngester) Handle(ctx context.Context, r servicestatus.Report) error {
	if r.DeviceID == "" {
		return sqsconsumer.Poison(errors.New("service-status report has no device_id"))
	}
	// Use cp-side wall clock as the authoritative "we received this"
	// timestamp — agent clocks drift; r.ReportedAt is informational only.
	// Per-service StateSince (agent best-effort) is preserved as-is.
	if err := i.writer.RecordServiceStates(ctx, r.DeviceID, r.Services, i.now()); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}
	// One line per stopped service — the Phase 2 alarm's metric filter
	// counts "service-status.stopped" occurrences. Emitting here (not
	// in the writer) keeps the alarm signal next to the protocol fact
	// rather than the storage fact: a write failure suppresses both
	// rightly. Running + unknown stay quiet to keep the noise low.
	log := i.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	for _, s := range r.Services {
		if s.State == service.StateStopped {
			log.Info("service-status.stopped",
				"device_id", r.DeviceID,
				"service", s.Name,
				"state_since", s.StateSince.UTC().Format(time.RFC3339),
				"correlation_id", r.CorrelationID,
			)
		}
	}
	return nil
}

// Compile-time checks that the service-status plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated                       = servicestatus.Report{}
	_ sqsconsumer.Handler[servicestatus.Report] = (*ServiceStatusIngester)(nil).Handle
)
