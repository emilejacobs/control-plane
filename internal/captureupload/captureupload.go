// Package captureupload is the agent-side client for the generic captures
// upload handshake (issue #8 pipeline, used by #9 scheduled snapshots and #10
// audio). A producer that runs *outside* a command handler — a scheduler tick,
// an fsnotify event — calls Upload, which:
//
//	publishes upload.request on cmd-result → awaits CP's upload.url grant
//	(delivered to HandleGrant by the dispatcher) → HTTP PUTs the bytes →
//	publishes upload.complete → returns the CP-minted S3 key.
//
// Upload blocks its caller's goroutine while awaiting the grant; the grant is
// delivered from the command-router goroutine, so the two never deadlock. This
// is why it must NOT be called from within a dispatcher handler (the
// CP-initiated camera.snapshot path embeds the URL in the command instead).
package captureupload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/upload"
)

// PublishFunc publishes a payload to an MQTT topic (the agent's transport).
type PublishFunc func(topic string, payload []byte) error

// PutFunc uploads body to a presigned URL with the given content type.
type PutFunc func(ctx context.Context, url, contentType string, body []byte) error

// Uploader runs the upload handshake. Safe for concurrent Upload calls — each
// correlation gets its own waiter.
type Uploader struct {
	deviceID string
	publish  PublishFunc
	put      PutFunc
	newID    func() string
	timeout  time.Duration

	mu      sync.Mutex
	waiters map[string]chan upload.URL
}

type Option func(*Uploader)

// WithNewID overrides the correlation-id minter (tests pin it).
func WithNewID(f func() string) Option { return func(u *Uploader) { u.newID = f } }

// WithTimeout sets how long Upload waits for the upload.url grant before failing.
func WithTimeout(d time.Duration) Option { return func(u *Uploader) { u.timeout = d } }

func New(deviceID string, publish PublishFunc, put PutFunc, opts ...Option) *Uploader {
	u := &Uploader{
		deviceID: deviceID,
		publish:  publish,
		put:      put,
		newID:    randomID,
		timeout:  30 * time.Second,
		waiters:  make(map[string]chan upload.URL),
	}
	for _, o := range opts {
		o(u)
	}
	return u
}

func (u *Uploader) cmdResultTopic() string { return "devices/" + u.deviceID + "/cmd-result" }

// Upload performs the full handshake and returns the stored object's key.
func (u *Uploader) Upload(ctx context.Context, kind, contentType string, data []byte, metadata map[string]any) (string, error) {
	req := upload.Request{Kind: kind, ContentType: contentType, SizeBytes: int64(len(data)), Metadata: metadata}
	if err := req.Validate(); err != nil {
		return "", err
	}

	corr := u.newID()
	ch := make(chan upload.URL, 1)
	u.register(corr, ch)
	defer u.unregister(corr)

	if err := u.publishResult(upload.TypeRequest, corr, req); err != nil {
		return "", fmt.Errorf("publish upload.request: %w", err)
	}

	var grant upload.URL
	select {
	case grant = <-ch:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(u.timeout):
		return "", fmt.Errorf("upload: timed out awaiting upload.url for %s", corr)
	}

	if err := u.put(ctx, grant.PutURL, contentType, data); err != nil {
		return "", fmt.Errorf("upload PUT: %w", err)
	}

	comp := upload.Complete{
		Kind:        kind,
		S3Key:       grant.S3Key,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		Metadata:    metadata,
	}
	if err := u.publishResult(upload.TypeComplete, corr, comp); err != nil {
		return "", fmt.Errorf("publish upload.complete: %w", err)
	}
	return grant.S3Key, nil
}

// HandleGrant is the dispatcher handler for the upload.url command. It routes
// the grant to the waiting Upload by correlation id; an unmatched grant is
// dropped (the request already timed out or completed). It returns no result —
// upload.url is a one-way reply, not a command awaiting an ACK.
func (u *Uploader) HandleGrant(_ context.Context, args json.RawMessage) (any, error) {
	var grant upload.URL
	if err := json.Unmarshal(args, &grant); err != nil {
		return nil, envelope.NewCodedError("upload.bad_grant", err.Error())
	}
	u.mu.Lock()
	ch := u.waiters[grant.CorrelationID]
	u.mu.Unlock()
	if ch != nil {
		select {
		case ch <- grant:
		default:
		}
	}
	return nil, nil
}

func (u *Uploader) register(corr string, ch chan upload.URL) {
	u.mu.Lock()
	u.waiters[corr] = ch
	u.mu.Unlock()
}

func (u *Uploader) unregister(corr string) {
	u.mu.Lock()
	delete(u.waiters, corr)
	u.mu.Unlock()
}

// publishResult wraps a payload in an envelope.Result on the cmd-result topic —
// the channel cp-ingest already consumes (it routes upload.request/complete by
// Type).
func (u *Uploader) publishResult(typ, corr string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env, err := json.Marshal(envelope.Result{
		Type:          typ,
		CorrelationID: corr,
		Success:       true,
		Result:        body,
	})
	if err != nil {
		return err
	}
	return u.publish(u.cmdResultTopic(), env)
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
