package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// camerasFileReader reads the agent-managed cameras file (the downstream copy
// of CP's inventory, written by the cameras.update applier) so the
// camera.snapshot handler can resolve a camera_id to its RTSP URL.
type camerasFileReader struct{ path string }

func newCamerasFileReader(path string) *camerasFileReader { return &camerasFileReader{path: path} }

func (r *camerasFileReader) Cameras(_ context.Context) ([]cameras.Camera, error) {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("read cameras file: %w", err)
	}
	var payload struct {
		Cameras []cameras.Camera `json:"cameras"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse cameras file: %w", err)
	}
	return payload.Cameras, nil
}

// ffmpegSnapshotter captures a single JPEG frame from an RTSP stream by shelling
// out to ffmpeg. TCP transport avoids the UDP packet loss that corrupts frames
// on busy LANs; a context deadline bounds a hung stream.
type ffmpegSnapshotter struct {
	timeout time.Duration
}

func newFFmpegSnapshotter() *ffmpegSnapshotter {
	return &ffmpegSnapshotter{timeout: 20 * time.Second}
}

func (s *ffmpegSnapshotter) Snapshot(ctx context.Context, rtspURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// -nostdin: never block on a tty. -frames:v 1: one frame. image2/mjpeg to
	// stdout so we capture the bytes without a temp file.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-frames:v", "1",
		"-q:v", "2",
		"-f", "image2",
		"-c:v", "mjpeg",
		"pipe:1",
	)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg snapshot: %w: %s", err, truncate(errOut.String(), 500))
	}
	if out.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg produced no frame: %s", truncate(errOut.String(), 500))
	}
	return out.Bytes(), nil
}

// httpUploader PUTs bytes to a presigned URL. The Content-Type must match the
// value CP signed or S3 rejects the request.
type httpUploader struct{ client *http.Client }

func newHTTPUploader() *httpUploader {
	return &httpUploader{client: &http.Client{Timeout: 30 * time.Second}}
}

func (u *httpUploader) Put(ctx context.Context, url, contentType string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("upload PUT status %d: %s", resp.StatusCode, snippet)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
