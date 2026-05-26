package edgeui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// fakeRunner is the RTSPRunner test seam. It returns a FrameStream
// that yields its pre-canned frames in order, then blocks on context
// cancellation (mirroring a real long-lived ffmpeg process).
type fakeRunner struct {
	frames     [][]byte
	errOnStart error
	frameGap   time.Duration
	cancelled  chan struct{}

	// observability: tests assert on URL passed in.
	mu         sync.Mutex
	lastURL    string
	startCount int
}

func newFakeRunner(frames [][]byte) *fakeRunner {
	return &fakeRunner{frames: frames, cancelled: make(chan struct{}, 1)}
}

func (f *fakeRunner) Start(ctx context.Context, rtspURL string) (FrameStream, error) {
	f.mu.Lock()
	f.lastURL = rtspURL
	f.startCount++
	f.mu.Unlock()
	if f.errOnStart != nil {
		return nil, f.errOnStart
	}
	return &fakeStream{ctx: ctx, frames: f.frames, gap: f.frameGap, cancelled: f.cancelled}, nil
}

type fakeStream struct {
	ctx       context.Context
	frames    [][]byte
	idx       int
	gap       time.Duration
	cancelled chan struct{}
}

func (s *fakeStream) NextFrame() ([]byte, error) {
	if s.idx > 0 && s.gap > 0 {
		select {
		case <-s.ctx.Done():
			select {
			case s.cancelled <- struct{}{}:
			default:
			}
			return nil, s.ctx.Err()
		case <-time.After(s.gap):
		}
	}
	if s.idx >= len(s.frames) {
		// Block until context cancellation (real ffmpeg stays alive).
		<-s.ctx.Done()
		select {
		case s.cancelled <- struct{}{}:
		default:
		}
		return nil, s.ctx.Err()
	}
	frame := s.frames[s.idx]
	s.idx++
	return frame, nil
}

func (s *fakeStream) Close() error { return nil }

// jpegFrame returns a minimal JPEG with SOI/EOI markers and a payload
// so the multipart parser sees distinct bodies. The MJPEG-over-HTTP
// boundary separates whole JPEGs; the handler does not need to
// re-parse JPEG markers itself.
func jpegFrame(payload byte, size int) []byte {
	out := make([]byte, 0, size+4)
	out = append(out, 0xFF, 0xD8) // SOI
	for i := 0; i < size; i++ {
		out = append(out, payload)
	}
	out = append(out, 0xFF, 0xD9) // EOI
	return out
}

func staticCameras(t *testing.T, list []cameras.Camera) CamerasReader {
	t.Helper()
	return CamerasReaderFunc(func() (map[string]cameras.Camera, error) {
		out := map[string]cameras.Camera{}
		for _, c := range list {
			out[c.CameraID] = c
		}
		return out, nil
	})
}

func TestPreviewHandler_HappyPath_WritesMultipart(t *testing.T) {
	frames := [][]byte{
		jpegFrame(0xAA, 64),
		jpegFrame(0xBB, 64),
		jpegFrame(0xCC, 64),
	}
	runner := newFakeRunner(frames)
	h := NewPreviewHandler(staticCameras(t, []cameras.Camera{
		{CameraID: "cam1", Label: "Drive-thru", RtspURL: "rtsp://host/stream"},
	}), runner)

	srv := httptest.NewServer(h)
	defer srv.Close()

	// Cancel the request after a short window so the runner goroutine
	// stops emitting (in production the operator closes the tab).
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/preview/cam1/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	if mediaType != "multipart/x-mixed-replace" {
		t.Errorf("media type: %s", mediaType)
	}
	if params["boundary"] != "ffmpeg" {
		t.Errorf("boundary: %s", params["boundary"])
	}

	mr := multipart.NewReader(resp.Body, "ffmpeg")
	got := 0
	for got < 3 {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Context cancellation surfaces here as a read error after
			// we've consumed frames — that's expected.
			break
		}
		body, _ := io.ReadAll(part)
		if !bytes.HasPrefix(body, []byte{0xFF, 0xD8}) {
			t.Errorf("part %d not a JPEG (got %x)", got, body[:2])
		}
		got++
	}
	if got != 3 {
		t.Errorf("expected 3 parts, got %d", got)
	}

	if runner.lastURL != "rtsp://host/stream" {
		t.Errorf("runner saw URL %q", runner.lastURL)
	}
}

func TestPreviewHandler_RunnerErrorOnStart_503(t *testing.T) {
	runner := newFakeRunner(nil)
	runner.errOnStart = errors.New("ffmpeg: no such device")
	h := NewPreviewHandler(staticCameras(t, []cameras.Camera{
		{CameraID: "cam1", RtspURL: "rtsp://host/stream"},
	}), runner)

	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/preview/cam1/stream")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	// Response is not multipart on the error path.
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		t.Errorf("error response must not be multipart, got %q", ct)
	}
}

func TestPreviewHandler_PassesCorrectRtspURLForCameraID(t *testing.T) {
	runner := newFakeRunner([][]byte{jpegFrame(0x42, 32)})
	h := NewPreviewHandler(staticCameras(t, []cameras.Camera{
		{CameraID: "cam1", RtspURL: "rtsp://drive-thru/stream"},
		{CameraID: "cam2", RtspURL: "rtsp://entry/stream"},
	}), runner)
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/preview/cam2/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if runner.lastURL != "rtsp://entry/stream" {
		t.Fatalf("expected runner to receive cam2's URL, got %q", runner.lastURL)
	}
}

func TestPreviewHandler_UnknownCameraID_404JSON(t *testing.T) {
	runner := newFakeRunner(nil)
	h := NewPreviewHandler(staticCameras(t, []cameras.Camera{
		{CameraID: "cam1", RtspURL: "rtsp://host/stream"},
	}), runner)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/preview/cam99/stream")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"code":"camera_not_found"`) {
		t.Fatalf("body did not include code=camera_not_found: %s", body)
	}
	if runner.startCount != 0 {
		t.Fatalf("runner should not have been started for unknown camera_id (calls: %d)", runner.startCount)
	}
}

func TestPreviewHandler_MissingCamerasFile_404(t *testing.T) {
	// CamerasReader that errors (mimics a malformed cameras.json or
	// a permissions issue) — the operator-facing failure is the same:
	// no URL to stream, surface 404.
	cr := CamerasReaderFunc(func() (map[string]cameras.Camera, error) {
		return nil, errors.New("read error")
	})
	runner := newFakeRunner(nil)
	h := NewPreviewHandler(cr, runner)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/preview/cam1/stream")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPreviewHandler_EmptyCamerasFile_404(t *testing.T) {
	// Pre-install device — cameras.json doesn't exist yet so the
	// reader returns an empty map. Any /preview/<camera_id>/stream
	// must 404.
	h := NewPreviewHandler(staticCameras(t, nil), newFakeRunner(nil))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/preview/cam1/stream")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// /preview/cam1 (no /stream suffix) is a SPA route — the API
// handler must not match it. The static handler (cycle 7) owns it.
func TestPreviewHandler_BareCameraURL_404(t *testing.T) {
	h := NewPreviewHandler(staticCameras(t, []cameras.Camera{
		{CameraID: "cam1", RtspURL: "rtsp://x/y"},
	}), newFakeRunner(nil))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/preview/cam1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for bare /preview/cam1 (SPA route), got %d", resp.StatusCode)
	}
}

func TestPreviewHandler_ClientCancel_PropagatesToRunner(t *testing.T) {
	// Long frame gap so the request is in mid-stream when the client
	// disconnects — that's the case we want exercised.
	runner := newFakeRunner([][]byte{jpegFrame(0x11, 32), jpegFrame(0x22, 32)})
	runner.frameGap = 50 * time.Millisecond

	h := NewPreviewHandler(staticCameras(t, []cameras.Camera{
		{CameraID: "cam1", RtspURL: "rtsp://x/y"},
	}), runner)
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/preview/cam1/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	// Read just enough to confirm streaming started, then cancel.
	br := bufio.NewReader(resp.Body)
	_, _ = br.ReadBytes('\n') // boundary line
	cancel()
	resp.Body.Close()

	select {
	case <-runner.cancelled:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatalf("runner was not cancelled after client disconnect")
	}
}
