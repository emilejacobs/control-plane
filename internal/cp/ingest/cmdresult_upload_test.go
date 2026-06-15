package ingest_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/upload"
)

type fakePresigner struct {
	mu          sync.Mutex
	key         string
	contentType string
	expiry      time.Duration
	calls       int
	err         error
}

func (f *fakePresigner) PutURL(_ context.Context, key, contentType string, expiry time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key, f.contentType, f.expiry = key, contentType, expiry
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return "https://s3.example/" + key + "?sig=x", nil
}

type fakePublisher struct {
	mu      sync.Mutex
	topic   string
	payload []byte
	calls   int
	err     error
}

func (f *fakePublisher) Publish(_ context.Context, topic string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.topic = topic
	f.payload = append([]byte(nil), payload...)
	f.calls++
	return f.err
}

// uploadIngester wires the captures upload pipeline onto a CmdResultIngester.
func uploadIngester(applier *recordingApplier, pre *fakePresigner, pub *fakePublisher) *ingest.CmdResultIngester {
	i := ingest.NewCmdResultIngester(applier, func() time.Time { return time.Unix(1700000000, 0).UTC() })
	i.Captures = applier
	i.Presigner = pre
	i.Publisher = pub
	i.NewID = func() string { return "cap-9" }
	return i
}

func uploadRequestResult(deviceID, corr string, req upload.Request) ingest.CmdResult {
	body, _ := json.Marshal(req)
	return ingest.CmdResult{
		Result:   envelope.Result{Type: upload.TypeRequest, CorrelationID: corr, Success: true, Result: body},
		DeviceID: deviceID,
	}
}

// upload.request → CP mints a CP-controlled key, presigns a PUT URL, and
// publishes upload.url back on the device's cmd topic (#8).
func TestCmdResultUploadRequestPresignsAndPublishesURL(t *testing.T) {
	applier := &recordingApplier{}
	pre := &fakePresigner{}
	pub := &fakePublisher{}
	i := uploadIngester(applier, pre, pub)

	err := i.Handle(context.Background(), uploadRequestResult("dev-1", "corr-1",
		upload.Request{Kind: upload.KindSnapshot, ContentType: "image/jpeg", SizeBytes: 100}))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	wantKey := "snapshots/dev-1/cap-9.jpg"
	if pre.calls != 1 || pre.key != wantKey || pre.contentType != "image/jpeg" {
		t.Fatalf("presign: calls=%d key=%q ct=%q", pre.calls, pre.key, pre.contentType)
	}
	if pre.expiry != 5*time.Minute {
		t.Errorf("presign expiry = %v, want 5m", pre.expiry)
	}
	if pub.calls != 1 || pub.topic != "devices/dev-1/cmd" {
		t.Fatalf("publish: calls=%d topic=%q", pub.calls, pub.topic)
	}
	var cmd envelope.Command
	if err := json.Unmarshal(pub.payload, &cmd); err != nil {
		t.Fatalf("published payload not a Command: %v", err)
	}
	if cmd.Type != upload.TypeURL || cmd.CorrelationID != "corr-1" {
		t.Errorf("cmd = {type:%q corr:%q}, want upload.url/corr-1", cmd.Type, cmd.CorrelationID)
	}
	var grant upload.URL
	if err := json.Unmarshal(cmd.Args, &grant); err != nil {
		t.Fatalf("cmd.Args not an upload.URL: %v", err)
	}
	if grant.S3Key != wantKey || grant.PutURL == "" {
		t.Errorf("grant = %+v, want key %q + a PUT URL", grant, wantKey)
	}
	// upload.request must not itself write a capture row — that waits for complete.
	if len(applier.captures) != 0 {
		t.Errorf("upload.request wrote %d captures, want 0", len(applier.captures))
	}
}

// upload.complete → CP indexes the device_captures row.
func TestCmdResultUploadCompleteInsertsCapture(t *testing.T) {
	applier := &recordingApplier{}
	i := uploadIngester(applier, &fakePresigner{}, &fakePublisher{})

	comp := upload.Complete{
		Kind:        upload.KindSnapshot,
		S3Key:       "snapshots/dev-1/cap-9.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   2048,
		Metadata:    map[string]any{"camera_id": "cam1"},
	}
	body, _ := json.Marshal(comp)
	err := i.Handle(context.Background(), ingest.CmdResult{
		Result:   envelope.Result{Type: upload.TypeComplete, CorrelationID: "corr-1", Success: true, Result: body},
		DeviceID: "dev-1",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(applier.captures) != 1 {
		t.Fatalf("captures = %d, want 1", len(applier.captures))
	}
	got := applier.captures[0]
	if got.DeviceID != "dev-1" || got.Kind != upload.KindSnapshot || got.S3Key != comp.S3Key ||
		got.ContentType != "image/jpeg" || got.SizeBytes != 2048 {
		t.Errorf("capture input = %+v", got)
	}
	if got.Metadata["camera_id"] != "cam1" {
		t.Errorf("metadata not threaded: %+v", got.Metadata)
	}
}

// A malformed/invalid upload.request is poison (no presign, no publish, no retry).
func TestCmdResultUploadRequestInvalidIsPoison(t *testing.T) {
	pre := &fakePresigner{}
	pub := &fakePublisher{}
	i := uploadIngester(&recordingApplier{}, pre, pub)

	err := i.Handle(context.Background(), uploadRequestResult("dev-1", "corr-1",
		upload.Request{Kind: "video", ContentType: "video/mp4", SizeBytes: 1}))
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Fatalf("invalid upload.request: err = %v, want poison", err)
	}
	if pre.calls != 0 || pub.calls != 0 {
		t.Errorf("invalid request should not presign/publish: pre=%d pub=%d", pre.calls, pub.calls)
	}
}

// With the captures pipeline unconfigured, upload messages are ignored (nil),
// not errors — so a cp-ingest without CAPTURES_BUCKET keeps draining the queue.
func TestCmdResultUploadIgnoredWhenUnconfigured(t *testing.T) {
	i := ingest.NewCmdResultIngester(&recordingApplier{}, nil)
	err := i.Handle(context.Background(), uploadRequestResult("dev-1", "corr-1",
		upload.Request{Kind: upload.KindSnapshot, ContentType: "image/jpeg", SizeBytes: 1}))
	if err != nil {
		t.Errorf("unconfigured upload.request should be ignored, got %v", err)
	}
}
