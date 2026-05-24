package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
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
	return nil
}

// Compile-time checks that the service-status plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated                       = servicestatus.Report{}
	_ sqsconsumer.Handler[servicestatus.Report] = (*ServiceStatusIngester)(nil).Handle
)
