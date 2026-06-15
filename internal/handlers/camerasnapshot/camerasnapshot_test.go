package camerasnapshot_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/handlers/camerasnapshot"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	protosnapshot "github.com/emilejacobs/control-plane/internal/protocol/camerasnapshot"
)

type fakeCameras struct {
	list []cameras.Camera
	err  error
}

func (f fakeCameras) Cameras(context.Context) ([]cameras.Camera, error) { return f.list, f.err }

type fakeSnapshotter struct {
	rtsp  string
	bytes []byte
	err   error
}

func (f *fakeSnapshotter) Snapshot(_ context.Context, rtsp string) ([]byte, error) {
	f.rtsp = rtsp
	return f.bytes, f.err
}

type fakeUploader struct {
	url, contentType string
	body             []byte
	err              error
}

func (f *fakeUploader) Put(_ context.Context, url, contentType string, body []byte) error {
	f.url, f.contentType, f.body = url, contentType, body
	return f.err
}

func argsJSON(t *testing.T, a protosnapshot.Args) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func twoCameras() []cameras.Camera {
	return []cameras.Camera{
		{CameraID: "cam1", Label: "Front", RtspURL: "rtsp://10.0.0.5/stream"},
		{CameraID: "cam2", Label: "Back", RtspURL: "rtsp://10.0.0.6/stream"},
	}
}

func TestSnapshotHappyPath(t *testing.T) {
	snap := &fakeSnapshotter{bytes: []byte("jpeg-bytes")}
	up := &fakeUploader{}
	h := camerasnapshot.New(fakeCameras{list: twoCameras()}, snap, up)

	out, err := h.Handle(context.Background(), argsJSON(t, protosnapshot.Args{
		CameraID: "cam2", S3Key: "snapshots/dev-1/x.jpg", PutURL: "https://s3.example/put",
	}))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Captured the right camera's stream and PUT to the presigned URL as jpeg.
	if snap.rtsp != "rtsp://10.0.0.6/stream" {
		t.Errorf("snapshot rtsp = %q, want cam2's", snap.rtsp)
	}
	if up.url != "https://s3.example/put" || up.contentType != protosnapshot.ContentType {
		t.Errorf("put url/type = %q/%q", up.url, up.contentType)
	}
	if string(up.body) != "jpeg-bytes" {
		t.Errorf("put body = %q", up.body)
	}
	res, ok := out.(protosnapshot.Result)
	if !ok {
		t.Fatalf("result type = %T", out)
	}
	if res.S3Key != "snapshots/dev-1/x.jpg" || res.SizeBytes != int64(len("jpeg-bytes")) || res.CameraID != "cam2" {
		t.Errorf("result = %+v", res)
	}
}

func TestSnapshotUnknownCamera(t *testing.T) {
	h := camerasnapshot.New(fakeCameras{list: twoCameras()}, &fakeSnapshotter{}, &fakeUploader{})
	_, err := h.Handle(context.Background(), argsJSON(t, protosnapshot.Args{
		CameraID: "cam9", S3Key: "k", PutURL: "u",
	}))
	assertCoded(t, err, protosnapshot.CodeUnknownCamera)
}

func TestSnapshotBadPayload(t *testing.T) {
	h := camerasnapshot.New(fakeCameras{list: twoCameras()}, &fakeSnapshotter{}, &fakeUploader{})
	_, err := h.Handle(context.Background(), json.RawMessage(`{"camera_id":""}`))
	assertCoded(t, err, protosnapshot.CodeBadPayload)
}

func TestSnapshotCaptureFailure(t *testing.T) {
	h := camerasnapshot.New(fakeCameras{list: twoCameras()},
		&fakeSnapshotter{err: errors.New("ffmpeg exited 1")}, &fakeUploader{})
	_, err := h.Handle(context.Background(), argsJSON(t, protosnapshot.Args{
		CameraID: "cam1", S3Key: "k", PutURL: "u",
	}))
	assertCoded(t, err, protosnapshot.CodeSnapshotFailed)
}

func TestSnapshotUploadFailure(t *testing.T) {
	h := camerasnapshot.New(fakeCameras{list: twoCameras()},
		&fakeSnapshotter{bytes: []byte("x")}, &fakeUploader{err: errors.New("403")})
	_, err := h.Handle(context.Background(), argsJSON(t, protosnapshot.Args{
		CameraID: "cam1", S3Key: "k", PutURL: "u",
	}))
	assertCoded(t, err, protosnapshot.CodeUploadFailed)
}

func assertCoded(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want coded error %q, got nil", wantCode)
	}
	var ce *envelope.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a CodedError", err)
	}
	if ce.Code != wantCode {
		t.Errorf("code = %q, want %q", ce.Code, wantCode)
	}
}
