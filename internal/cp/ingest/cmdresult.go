package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/camerasnapshot"
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
	"github.com/emilejacobs/control-plane/internal/protocol/upload"
)

// uploadURLTTL is how long the presigned PUT URL CP hands the agent stays
// valid — long enough to upload a snapshot/recording, short enough to limit a
// leaked URL's blast radius (#8).
const uploadURLTTL = 5 * time.Minute

// CaptureWriter indexes a freshly-uploaded artifact. *registry.Registry
// satisfies it. Separate from CmdResultWriter so the captures pipeline is
// optional wiring.
type CaptureWriter interface {
	InsertCapture(ctx context.Context, in registry.CaptureInput) (registry.Capture, error)
}

// uploadPresigner mints the short-lived PUT URL the agent uploads to.
// *captures.S3Presigner satisfies it.
type uploadPresigner interface {
	PutURL(ctx context.Context, key, contentType string, expiry time.Duration) (string, error)
}

// cmdPublisher publishes a command back to a device's cmd topic.
// *iotpublisher.AWS satisfies it.
type cmdPublisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
}

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

	// Captures pipeline (#8). All four must be set to enable upload.request /
	// upload.complete handling; if any is nil the handler logs and ignores
	// those messages so a cp-ingest without CAPTURES_BUCKET keeps draining.
	Captures  CaptureWriter
	Presigner uploadPresigner
	Publisher cmdPublisher
	// NewID mints the capture id that becomes the S3 key component. Defaults
	// to a random uuid when nil but Captures is set.
	NewID func() string
}

// capturesEnabled reports whether the full upload pipeline is wired.
func (i *CmdResultIngester) capturesEnabled() bool {
	return i.Captures != nil && i.Presigner != nil && i.Publisher != nil
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
	case upload.TypeRequest:
		return i.handleUploadRequest(ctx, r, log)
	case upload.TypeComplete:
		return i.handleUploadComplete(ctx, r, log)
	case "camera.snapshot":
		return i.handleCameraSnapshot(ctx, r, log)
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

// handleUploadRequest mints a CP-controlled S3 key, presigns a short-lived PUT
// URL, and publishes upload.url back on the device's cmd topic (#8). An invalid
// request is poison (the agent can't fix it by resending); a transient presign
// or publish failure propagates so SQS redelivers.
func (i *CmdResultIngester) handleUploadRequest(ctx context.Context, r CmdResult, log *slog.Logger) error {
	if !i.capturesEnabled() {
		log.Warn("upload.request ignored — captures pipeline not configured", "device_id", r.DeviceID)
		return nil
	}

	var req upload.Request
	if err := json.Unmarshal(r.Result.Result, &req); err != nil {
		return sqsconsumer.Poison(err)
	}
	if err := req.Validate(); err != nil {
		return sqsconsumer.Poison(err)
	}

	newID := i.NewID
	if newID == nil {
		newID = randomID
	}
	key, err := upload.S3Key(req.Kind, r.DeviceID, newID(), req.ContentType)
	if err != nil {
		return sqsconsumer.Poison(err)
	}

	putURL, err := i.Presigner.PutURL(ctx, key, req.ContentType, uploadURLTTL)
	if err != nil {
		return err // transient — let SQS redeliver
	}

	args, err := json.Marshal(upload.URL{CorrelationID: r.CorrelationID, S3Key: key, PutURL: putURL})
	if err != nil {
		return sqsconsumer.Poison(err)
	}
	cmd, err := json.Marshal(envelope.Command{
		CorrelationID: r.CorrelationID,
		Type:          upload.TypeURL,
		Args:          args,
	})
	if err != nil {
		return sqsconsumer.Poison(err)
	}
	if err := i.Publisher.Publish(ctx, "devices/"+r.DeviceID+"/cmd", cmd); err != nil {
		return err // transient
	}
	log.Info("upload.url issued",
		"device_id", r.DeviceID,
		"correlation_id", r.CorrelationID,
		"kind", req.Kind,
		"s3_key", key,
	)
	return nil
}

// handleUploadComplete indexes a device_captures row once the agent confirms
// its PUT landed (#8).
func (i *CmdResultIngester) handleUploadComplete(ctx context.Context, r CmdResult, log *slog.Logger) error {
	if !i.capturesEnabled() {
		log.Warn("upload.complete ignored — captures pipeline not configured", "device_id", r.DeviceID)
		return nil
	}

	var comp upload.Complete
	if err := json.Unmarshal(r.Result.Result, &comp); err != nil {
		return sqsconsumer.Poison(err)
	}
	if comp.S3Key == "" || comp.Kind == "" {
		return sqsconsumer.Poison(errors.New("upload.complete missing s3_key or kind"))
	}

	c, err := i.Captures.InsertCapture(ctx, registry.CaptureInput{
		DeviceID:    r.DeviceID,
		Kind:        comp.Kind,
		S3Key:       comp.S3Key,
		ContentType: comp.ContentType,
		SizeBytes:   comp.SizeBytes,
		Metadata:    comp.Metadata,
	})
	if err != nil {
		return err // transient — SQS redelivers (FK to a since-deleted device DLQs after retries)
	}
	log.Info("capture indexed",
		"device_id", r.DeviceID,
		"capture_id", c.ID,
		"kind", c.Kind,
		"s3_key", c.S3Key,
	)
	return nil
}

// handleCameraSnapshot indexes a snapshot capture from a camera.snapshot ACK
// (#8 Slice B). camera.snapshot embeds the presigned PUT in the command, so by
// the time the agent ACKs the bytes are already in S3 — CP just records the row.
// A failure ACK is logged, not retried (the dashboard surfaces no fresh shot).
func (i *CmdResultIngester) handleCameraSnapshot(ctx context.Context, r CmdResult, log *slog.Logger) error {
	if !i.capturesEnabled() {
		log.Warn("camera.snapshot ignored — captures pipeline not configured", "device_id", r.DeviceID)
		return nil
	}
	if !r.Success {
		code, message := "", ""
		if r.Error != nil {
			code, message = r.Error.Code, r.Error.Message
		}
		log.Warn("camera.snapshot ACK failure",
			"device_id", r.DeviceID,
			"correlation_id", r.CorrelationID,
			"error_code", code,
			"error_message", message,
		)
		return nil
	}

	var res camerasnapshot.Result
	if err := json.Unmarshal(r.Result.Result, &res); err != nil {
		return sqsconsumer.Poison(err)
	}
	if res.S3Key == "" {
		return sqsconsumer.Poison(errors.New("camera.snapshot ACK missing s3_key"))
	}

	c, err := i.Captures.InsertCapture(ctx, registry.CaptureInput{
		DeviceID:    r.DeviceID,
		Kind:        upload.KindSnapshot,
		S3Key:       res.S3Key,
		ContentType: camerasnapshot.ContentType,
		SizeBytes:   res.SizeBytes,
		Metadata:    map[string]any{"camera_id": res.CameraID},
	})
	if err != nil {
		return err
	}
	log.Info("snapshot captured",
		"device_id", r.DeviceID,
		"capture_id", c.ID,
		"camera_id", res.CameraID,
		"s3_key", res.S3Key,
	)
	return nil
}

// randomID mints a 128-bit hex token for the S3 key when NewID is unset.
func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Compile-time checks that the cmd-result plumbing fits the consumer.
var (
	_ sqsconsumer.Correlated         = CmdResult{}
	_ sqsconsumer.Handler[CmdResult] = (*CmdResultIngester)(nil).Handle
)
