package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/emilejacobs/control-plane/internal/cp/captures"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/upload"
)

// capturingPublisher records the single command cp-ingest publishes back to the
// device (the upload.url grant) so the test can drive the PUT.
type capturingPublisher struct {
	mu      sync.Mutex
	topic   string
	payload []byte
}

func (p *capturingPublisher) Publish(_ context.Context, topic string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topic = topic
	p.payload = append([]byte(nil), payload...)
	return nil
}

// The captures write pipeline end-to-end against real Postgres + moto S3 (#8):
// upload.request → CP presigns a PUT + publishes upload.url → agent PUTs bytes →
// upload.complete → a device_captures row. This is the issue's headline AC.
func TestCapturesUploadRoundTrip(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-cap-01", "80000000-0000-0000-0000-000000000001")

	s3Client := startMotoS3(t, ctx)
	bucket := "uknomi-cp-captures-test"
	if _, err := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	pub := &capturingPublisher{}
	ing := ingest.NewCmdResultIngester(srv.Registry, nil)
	ing.Captures = srv.Registry
	ing.Presigner = captures.NewS3Presigner(s3.NewPresignClient(s3Client), bucket)
	ing.Publisher = pub
	ing.NewID = func() string { return "cap-test-1" }

	// 1. upload.request → CP presigns + publishes upload.url.
	reqBody, _ := json.Marshal(upload.Request{Kind: upload.KindSnapshot, ContentType: "image/jpeg", SizeBytes: 5})
	if err := ing.Handle(ctx, ingest.CmdResult{
		Result:   envelope.Result{Type: upload.TypeRequest, CorrelationID: "c1", Success: true, Result: reqBody},
		DeviceID: deviceID,
	}); err != nil {
		t.Fatalf("handle upload.request: %v", err)
	}

	if pub.topic != "devices/"+deviceID+"/cmd" {
		t.Fatalf("upload.url published to %q", pub.topic)
	}
	var cmd envelope.Command
	if err := json.Unmarshal(pub.payload, &cmd); err != nil {
		t.Fatalf("published cmd: %v", err)
	}
	var grant upload.URL
	if err := json.Unmarshal(cmd.Args, &grant); err != nil {
		t.Fatalf("grant args: %v", err)
	}
	wantKey := "snapshots/" + deviceID + "/cap-test-1.jpg"
	if grant.S3Key != wantKey {
		t.Fatalf("grant key = %q, want %q", grant.S3Key, wantKey)
	}

	// 2. Agent PUTs the bytes to the presigned URL. Content-Type must match the
	// signed value or S3 rejects the signature.
	payload := []byte("\xff\xd8\xff\xe0 jpeg bytes")
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.PutURL, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	putReq.Header.Set("Content-Type", "image/jpeg")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT to presigned URL: %v", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT status = %d, body: %s", putResp.StatusCode, body)
	}

	// 3. The object actually landed in S3 under the CP-minted key.
	obj, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(grant.S3Key)})
	if err != nil {
		t.Fatalf("GetObject %s: %v", grant.S3Key, err)
	}
	stored, _ := io.ReadAll(obj.Body)
	if !bytes.Equal(stored, payload) {
		t.Fatalf("stored bytes = %q, want %q", stored, payload)
	}

	// 4. upload.complete → a device_captures row.
	compBody, _ := json.Marshal(upload.Complete{
		Kind:        upload.KindSnapshot,
		S3Key:       grant.S3Key,
		ContentType: "image/jpeg",
		SizeBytes:   int64(len(payload)),
		Metadata:    map[string]any{"camera_id": "cam1"},
	})
	if err := ing.Handle(ctx, ingest.CmdResult{
		Result:   envelope.Result{Type: upload.TypeComplete, CorrelationID: "c1", Success: true, Result: compBody},
		DeviceID: deviceID,
	}); err != nil {
		t.Fatalf("handle upload.complete: %v", err)
	}

	caps, err := srv.Registry.ListCaptures(staffCtx(ctx), deviceID, upload.KindSnapshot)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(caps) != 1 {
		t.Fatalf("captures = %d, want 1", len(caps))
	}
	got := caps[0]
	if got.S3Key != wantKey || got.ContentType != "image/jpeg" || got.SizeBytes != int64(len(payload)) {
		t.Errorf("capture row = %+v", got)
	}
	if got.Metadata["camera_id"] != "cam1" {
		t.Errorf("capture metadata = %+v", got.Metadata)
	}
}
