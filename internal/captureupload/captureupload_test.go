package captureupload_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/captureupload"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/upload"
)

type pub struct {
	mu   sync.Mutex
	msgs []envelope.Result
}

func (p *pub) publish(_ string, payload []byte) error {
	var r envelope.Result
	if err := json.Unmarshal(payload, &r); err != nil {
		return err
	}
	p.mu.Lock()
	p.msgs = append(p.msgs, r)
	p.mu.Unlock()
	return nil
}

func (p *pub) first(typ string) (envelope.Result, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.msgs {
		if m.Type == typ {
			return m, true
		}
	}
	return envelope.Result{}, false
}

// waitForRequest blocks until the uploader has published its upload.request
// (so the waiter is registered before the test delivers the grant).
func waitForRequest(t *testing.T, p *pub) envelope.Result {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := p.first(upload.TypeRequest); ok {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("upload.request was never published")
	return envelope.Result{}
}

// The agent-initiated upload handshake (#9): the uploader publishes
// upload.request, waits for CP's upload.url grant (delivered via the dispatcher
// to HandleGrant), PUTs the bytes, then publishes upload.complete and returns
// the CP-minted key.
func TestUploaderHandshake(t *testing.T) {
	p := &pub{}
	var (
		mu                 sync.Mutex
		putURL, putCT      string
		putBody            []byte
	)
	put := func(_ context.Context, url, ct string, body []byte) error {
		mu.Lock()
		putURL, putCT, putBody = url, ct, append([]byte(nil), body...)
		mu.Unlock()
		return nil
	}
	u := captureupload.New("dev-1", p.publish, put, captureupload.WithNewID(func() string { return "u1" }))

	type res struct {
		key string
		err error
	}
	done := make(chan res, 1)
	go func() {
		k, e := u.Upload(context.Background(), upload.KindAudio, "audio/wav", []byte("wavdata"), map[string]any{"parent": "rec-1"})
		done <- res{k, e}
	}()

	// The request must carry the kind/size/correlation.
	req := waitForRequest(t, p)
	if req.CorrelationID != "u1" {
		t.Fatalf("request correlation = %q, want u1", req.CorrelationID)
	}
	var got upload.Request
	if err := json.Unmarshal(req.Result, &got); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if got.Kind != upload.KindAudio || got.ContentType != "audio/wav" || got.SizeBytes != int64(len("wavdata")) {
		t.Errorf("request = %+v", got)
	}

	// CP replies with the grant (carrying the correlation, as cp-ingest sets).
	grant, _ := json.Marshal(upload.URL{CorrelationID: "u1", S3Key: "audio/dev-1/u1.wav", PutURL: "https://s3.example/put"})
	if _, err := u.HandleGrant(context.Background(), grant); err != nil {
		t.Fatalf("HandleGrant: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Upload: %v", r.err)
		}
		if r.key != "audio/dev-1/u1.wav" {
			t.Errorf("returned key = %q", r.key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Upload did not return after the grant")
	}

	mu.Lock()
	if putURL != "https://s3.example/put" || putCT != "audio/wav" || string(putBody) != "wavdata" {
		t.Errorf("PUT url/ct/body = %q/%q/%q", putURL, putCT, putBody)
	}
	mu.Unlock()

	comp, ok := p.first(upload.TypeComplete)
	if !ok {
		t.Fatal("upload.complete was never published")
	}
	var c upload.Complete
	_ = json.Unmarshal(comp.Result, &c)
	if c.S3Key != "audio/dev-1/u1.wav" || c.SizeBytes != int64(len("wavdata")) || c.Metadata["parent"] != "rec-1" {
		t.Errorf("complete = %+v", c)
	}
}

// A grant that never arrives times out rather than blocking forever.
func TestUploaderTimesOutWithoutGrant(t *testing.T) {
	p := &pub{}
	put := func(context.Context, string, string, []byte) error { return nil }
	u := captureupload.New("dev-1", p.publish, put,
		captureupload.WithNewID(func() string { return "u1" }),
		captureupload.WithTimeout(50*time.Millisecond))

	_, err := u.Upload(context.Background(), upload.KindSnapshot, "image/jpeg", []byte("x"), nil)
	if err == nil {
		t.Fatal("expected a timeout error when no grant arrives")
	}
}

// A grant for an unknown correlation is dropped, not a panic.
func TestUploaderHandleGrantUnknownCorrelation(t *testing.T) {
	p := &pub{}
	put := func(context.Context, string, string, []byte) error { return nil }
	u := captureupload.New("dev-1", p.publish, put)
	grant, _ := json.Marshal(upload.URL{CorrelationID: "nobody", S3Key: "k", PutURL: "u"})
	if _, err := u.HandleGrant(context.Background(), grant); err != nil {
		t.Errorf("HandleGrant for unknown correlation should be a no-op, got %v", err)
	}
}

// An invalid upload request is rejected before any publish.
func TestUploaderRejectsInvalidRequest(t *testing.T) {
	p := &pub{}
	put := func(context.Context, string, string, []byte) error { return nil }
	u := captureupload.New("dev-1", p.publish, put)
	_, err := u.Upload(context.Background(), "video", "video/mp4", []byte("x"), nil)
	if err == nil {
		t.Fatal("expected rejection of an unknown kind")
	}
	if _, ok := p.first(upload.TypeRequest); ok {
		t.Error("invalid request should not be published")
	}
	_ = errors.Unwrap(err)
}
