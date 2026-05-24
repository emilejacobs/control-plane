package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/envelope"
)

// CmdResult is the cp-ingest wrapper around envelope.Result. The
// embedded envelope carries the agent's ACK; DeviceID comes from the
// IoT Rule's `SELECT *, topic(2) as device_id` since cmd-result
// payloads on the wire don't natively carry it (the topic does).
//
// Implements sqsconsumer.Correlated via the embedded Result.
type CmdResult struct {
	envelope.Result
	DeviceID string `json:"device_id"`
}

// Correlation satisfies sqsconsumer.Correlated.
func (r CmdResult) Correlation() string { return r.CorrelationID }

// ConfigUpdateAckWriter is the registry slice the cmd-result handler
// needs for Phase 2 slice 2's config.update ACK flow. *registry.Registry
// satisfies it.
type ConfigUpdateAckWriter interface {
	RecordServiceConfigApplied(ctx context.Context, deviceID, correlationID string, at time.Time) error
}

// CmdResultIngester is the sqsconsumer.Handler[CmdResult]. Routes
// by Type field — slice 2 only handles "config.update"; other types
// are silently ignored so Phase 3 can add handlers without breaking
// existing flow (the Result envelope is the shared shape).
type CmdResultIngester struct {
	writer ConfigUpdateAckWriter
	now    func() time.Time
	// Logger receives a warn-level line for every failure ACK and
	// (at info) for every successful config.update apply. Nil
	// defaults to a discard logger.
	Logger *slog.Logger
}

func NewCmdResultIngester(w ConfigUpdateAckWriter, now func() time.Time) *CmdResultIngester {
	if now == nil {
		now = time.Now
	}
	return &CmdResultIngester{writer: w, now: now}
}

// Handle is the sqsconsumer.Handler[CmdResult].
//
// Per the heartbeat / service-status pattern: an empty device_id or
// an unknown device is poison (DLQ-bound, no retry); transient writer
// errors propagate so SQS redelivers.
func (i *CmdResultIngester) Handle(ctx context.Context, r CmdResult) error {
	if r.DeviceID == "" {
		return sqsconsumer.Poison(errors.New("cmd-result has no device_id (IoT Rule topic(2) injection missing?)"))
	}

	log := i.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	switch r.Type {
	case "config.update":
		return i.handleConfigUpdate(ctx, r, log)
	default:
		// Other cmd types are valid envelopes but not in slice 2's
		// scope; Phase 3 will add per-type handlers. Silently
		// ignored so the queue doesn't back up on noise.
		return nil
	}
}

func (i *CmdResultIngester) handleConfigUpdate(ctx context.Context, r CmdResult, log *slog.Logger) error {
	if !r.Success {
		// Failure ACK: log the agent's error code; no DB write (the
		// override on the cp side stays as the operator set it, and
		// the dashboard surfaces the absence of a fresh apply
		// timestamp as "pending / not applied"). The message is
		// considered handled — no retry.
		code, message := "", ""
		if r.Error != nil {
			code = r.Error.Code
			message = r.Error.Message
		}
		log.Warn("config.update ACK failure",
			"device_id", r.DeviceID,
			"correlation_id", r.CorrelationID,
			"error_code", code,
			"error_message", message,
		)
		return nil
	}

	if err := i.writer.RecordServiceConfigApplied(ctx, r.DeviceID, r.CorrelationID, i.now()); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}
	log.Info("config.update applied",
		"device_id", r.DeviceID,
		"correlation_id", r.CorrelationID,
	)
	return nil
}

// Compile-time checks that the cmd-result plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated         = CmdResult{}
	_ sqsconsumer.Handler[CmdResult] = (*CmdResultIngester)(nil).Handle
)
