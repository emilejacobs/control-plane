package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
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

// CmdResultWriter is the registry slice the cmd-result handler needs.
// Covers slice 2 (config.update), slice 3 (log.tail), and Edge UI
// rework (cameras.update, network.scan) ACK flows. *registry.Registry
// satisfies every method.
type CmdResultWriter interface {
	// Slice 2: config.update ACK stamps last_applied_* on the device row.
	RecordServiceConfigApplied(ctx context.Context, deviceID, correlationID string, at time.Time) error
	// Slice 3: log.tail success — updates the pending row with content + truncation.
	CompleteLogTail(ctx context.Context, c registry.LogTailCompletion) error
	// Slice 3: log.tail failure — updates the pending row with error code + message.
	FailLogTail(ctx context.Context, f registry.LogTailFailure) error
	// Edge UI rework (issue #2): cameras.update ACK stamps the
	// cameras_last_applied_* mirror columns on the device row.
	RecordCamerasApplied(ctx context.Context, deviceID, correlationID string, at time.Time) error
	// Edge UI rework (issue #3): network.scan success — updates the
	// pending device_network_scans row with the agent's hosts list.
	CompleteNetworkScan(ctx context.Context, c registry.NetworkScanCompletion) error
	// Edge UI rework (issue #3): network.scan failure — updates the
	// pending row with the agent's error code/message.
	FailNetworkScan(ctx context.Context, f registry.NetworkScanFailure) error
}

// CmdResultIngester is the sqsconsumer.Handler[CmdResult]. Routes
// by Type field — slice 2 handles "config.update", slice 3 adds
// "log.tail"; other types are silently ignored so Phase 3 can keep
// adding handlers without breaking existing flow.
type CmdResultIngester struct {
	writer CmdResultWriter
	now    func() time.Time
	// Logger receives a warn-level line for every failure ACK and
	// (at info) for every successful apply. Nil defaults to a discard
	// logger.
	Logger *slog.Logger
}

func NewCmdResultIngester(w CmdResultWriter, now func() time.Time) *CmdResultIngester {
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
	case "log.tail":
		return i.handleLogTail(ctx, r, log)
	case "cameras.update":
		return i.handleCamerasUpdate(ctx, r, log)
	case "network.scan":
		return i.handleNetworkScan(ctx, r, log)
	default:
		// Other cmd types are valid envelopes but not in scope here;
		// Phase 3 will add per-type handlers. Silently ignored so the
		// queue doesn't back up on noise.
		return nil
	}
}

// handleLogTail routes a log.tail ACK to CompleteLogTail (success)
// or FailLogTail (error). Both produce DB writes; either way the
// dashboard's poller sees the row transition out of "pending" on its
// next fetch. ErrLogTailNotFound is poison — the request row went
// away (sweeper ran early, or the ACK is from a row the agent had
// cached past our retention). Either way, no point retrying.
func (i *CmdResultIngester) handleLogTail(ctx context.Context, r CmdResult, log *slog.Logger) error {
	returnedAt := i.now()

	if !r.Success {
		code, message := "", ""
		if r.Error != nil {
			code = r.Error.Code
			message = r.Error.Message
		}
		log.Warn("log.tail ACK failure",
			"device_id", r.DeviceID,
			"correlation_id", r.CorrelationID,
			"error_code", code,
			"error_message", message,
		)
		err := i.writer.FailLogTail(ctx, registry.LogTailFailure{
			CorrelationID: r.CorrelationID,
			ErrorCode:     code,
			ErrorMessage:  message,
			ReturnedAt:    returnedAt,
		})
		if err != nil {
			if errors.Is(err, registry.ErrLogTailNotFound) {
				return sqsconsumer.Poison(err)
			}
			return err
		}
		return nil
	}

	// Success: unmarshal the protologtail.Response from the embedded
	// envelope's Result field (r.Result.Result — the outer r.Result
	// resolves to the embedded envelope.Result struct).
	var resp logTailResultPayload
	if err := json.Unmarshal(r.Result.Result, &resp); err != nil {
		return sqsconsumer.Poison(err)
	}
	if err := i.writer.CompleteLogTail(ctx, registry.LogTailCompletion{
		CorrelationID: r.CorrelationID,
		Content:       resp.Content,
		Truncated:     resp.Truncated,
		TruncatedFrom: resp.TruncatedFrom,
		ReturnedAt:    returnedAt,
	}); err != nil {
		if errors.Is(err, registry.ErrLogTailNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}
	log.Info("log.tail applied",
		"device_id", r.DeviceID,
		"correlation_id", r.CorrelationID,
		"content_bytes", len(resp.Content),
		"truncated", resp.Truncated,
	)
	return nil
}

// logTailResultPayload is the shape of the protologtail.Response
// embedded in envelope.Result.Result for a log.tail ACK. Mirrors the
// fields the agent sends; defined here as a local type so the ingest
// package doesn't depend on the agent protocol package directly (it's
// just JSON shape parity).
type logTailResultPayload struct {
	Content       string `json:"content"`
	Truncated     bool   `json:"truncated"`
	TruncatedFrom int    `json:"truncated_from"`
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

// handleCamerasUpdate stamps the cameras_last_applied_* mirror
// columns when the agent ACKs a cameras.update cmd. Failure ACKs
// log + return nil (same posture as config.update — the override
// stays as the operator set it; the dashboard surfaces the absence
// of a fresh apply timestamp as "pending").
func (i *CmdResultIngester) handleCamerasUpdate(ctx context.Context, r CmdResult, log *slog.Logger) error {
	if !r.Success {
		code, message := "", ""
		if r.Error != nil {
			code = r.Error.Code
			message = r.Error.Message
		}
		log.Warn("cameras.update ACK failure",
			"device_id", r.DeviceID,
			"correlation_id", r.CorrelationID,
			"error_code", code,
			"error_message", message,
		)
		return nil
	}

	if err := i.writer.RecordCamerasApplied(ctx, r.DeviceID, r.CorrelationID, i.now()); err != nil {
		if errors.Is(err, registry.ErrDeviceNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}
	log.Info("cameras.update applied",
		"device_id", r.DeviceID,
		"correlation_id", r.CorrelationID,
	)
	return nil
}

// handleNetworkScan routes a network.scan ACK to CompleteNetworkScan
// (success) or FailNetworkScan (error). Both produce DB writes; either
// way the dashboard's poller sees the row transition out of "pending"
// on its next fetch. ErrNetworkScanNotFound is poison — the request
// row went away (sweeper ran early, or the ACK is from a row the agent
// cached past retention). Either way, no point retrying.
func (i *CmdResultIngester) handleNetworkScan(ctx context.Context, r CmdResult, log *slog.Logger) error {
	returnedAt := i.now()

	if !r.Success {
		code, message := "", ""
		if r.Error != nil {
			code = r.Error.Code
			message = r.Error.Message
		}
		log.Warn("network.scan ACK failure",
			"device_id", r.DeviceID,
			"correlation_id", r.CorrelationID,
			"error_code", code,
			"error_message", message,
		)
		err := i.writer.FailNetworkScan(ctx, registry.NetworkScanFailure{
			CorrelationID: r.CorrelationID,
			ErrorCode:     code,
			ErrorMessage:  message,
			ReturnedAt:    returnedAt,
		})
		if err != nil {
			if errors.Is(err, registry.ErrNetworkScanNotFound) {
				return sqsconsumer.Poison(err)
			}
			return err
		}
		return nil
	}

	// Success: unmarshal the networkscan.Response from the embedded
	// envelope's Result and hand the hosts list straight to the
	// registry. The protocol shape is the wire shape; the ingest layer
	// is a thin router.
	var resp networkscan.Response
	if err := json.Unmarshal(r.Result.Result, &resp); err != nil {
		return sqsconsumer.Poison(err)
	}
	if err := i.writer.CompleteNetworkScan(ctx, registry.NetworkScanCompletion{
		CorrelationID: r.CorrelationID,
		Hosts:         resp.Hosts,
		ReturnedAt:    returnedAt,
	}); err != nil {
		if errors.Is(err, registry.ErrNetworkScanNotFound) {
			return sqsconsumer.Poison(err)
		}
		return err
	}
	log.Info("network.scan applied",
		"device_id", r.DeviceID,
		"correlation_id", r.CorrelationID,
		"host_count", len(resp.Hosts),
	)
	return nil
}

// Compile-time checks that the cmd-result plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated         = CmdResult{}
	_ sqsconsumer.Handler[CmdResult] = (*CmdResultIngester)(nil).Handle
)
