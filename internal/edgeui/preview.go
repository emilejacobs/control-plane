package edgeui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// CamerasReader is the seam the preview handler uses to look up an
// RTSP URL by camera_id. Production wires a closure over ReadCameras;
// tests pass a CamerasReaderFunc.
type CamerasReader interface {
	Cameras() (map[string]cameras.Camera, error)
}

// CamerasReaderFunc adapts a bare function into the CamerasReader
// interface — same shape as http.HandlerFunc.
type CamerasReaderFunc func() (map[string]cameras.Camera, error)

func (f CamerasReaderFunc) Cameras() (map[string]cameras.Camera, error) { return f() }

// RTSPRunner is the seam over the subprocess that produces MJPEG
// frames from an RTSP source. Start returns a FrameStream — a
// frame-by-frame reader over whole JPEGs. Cancelling ctx kills the
// subprocess. Production wires FFmpegRunner; tests substitute an
// in-memory fake.
//
// The frame-shaped seam (NextFrame returns one JPEG at a time) keeps
// the handler simple — it wraps each frame in a multipart prelude
// without re-parsing JPEG markers itself. The production runner
// splits ffmpeg's continuous-JPEG output on SOI markers; tests
// deliver pre-split frames directly.
type RTSPRunner interface {
	Start(ctx context.Context, rtspURL string) (FrameStream, error)
}

// FrameStream is one JPEG at a time. NextFrame blocks until a frame
// is ready or ctx is cancelled / EOF. The returned slice is owned by
// the stream — the caller must finish using it before the next call.
type FrameStream interface {
	NextFrame() ([]byte, error)
	Close() error
}

// PreviewHandler is the Edge UI's only API surface today. The Next.js
// SPA at /preview/<camera_id> embeds an <img src="…/stream"> pointing
// here; the handler resolves camera_id against the local cameras file,
// spawns the RTSP runner, and proxies its stdout as a
// multipart/x-mixed-replace response. The /stream suffix on the URL
// is what disambiguates the API route from the SPA route the static
// handler serves.
type PreviewHandler struct {
	cameras CamerasReader
	runner  RTSPRunner
}

// NewPreviewHandler returns an http.Handler that serves
// /preview/<camera_id>/stream. The cameras reader is invoked on each
// request — the agent rewrites the cameras file atomically, so a fresh
// read picks up edits without restart.
func NewPreviewHandler(cr CamerasReader, runner RTSPRunner) *PreviewHandler {
	return &PreviewHandler{cameras: cr, runner: runner}
}

// boundary is fixed at "ffmpeg" — both <img> and <video> elements
// parse multipart/x-mixed-replace without caring about the boundary
// string's content, so a stable literal keeps the response shape
// predictable in test fixtures.
const boundary = "ffmpeg"

func (h *PreviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cameraID, ok := parseCameraID(r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "camera_not_found"})
		return
	}
	cams, err := h.cameras.Cameras()
	if err != nil {
		// A read-error on the cameras file is operationally the same
		// as a 404 from the operator's perspective: there's no URL
		// the handler can stream. Phase-3-style structured errors are
		// not in scope for v1.
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "camera_not_found"})
		return
	}
	cam, found := cams[cameraID]
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "camera_not_found"})
		return
	}

	stream, err := h.runner.Start(r.Context(), cam.RtspURL)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code":    "rtsp_unavailable",
			"message": err.Error(),
		})
		return
	}
	defer stream.Close()

	// Read the first frame before writing the multipart header so an
	// immediate failure (ffmpeg can't reach the camera) surfaces as
	// a 503 instead of a half-baked multipart response.
	first, err := stream.NextFrame()
	if err != nil || len(first) == 0 {
		msg := "no frames"
		if err != nil {
			msg = err.Error()
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code":    "rtsp_unavailable",
			"message": msg,
		})
		return
	}

	w.Header().Set("Content-Type", fmt.Sprintf("multipart/x-mixed-replace; boundary=%s", boundary))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	if !writePart(w, flusher, first) {
		return
	}
	for {
		frame, err := stream.NextFrame()
		if err != nil || len(frame) == 0 {
			return
		}
		if !writePart(w, flusher, frame) {
			return
		}
	}
}

// writePart writes one frame as a multipart segment and flushes the
// response. Returns false if the underlying write failed (client
// disconnected, etc).
func writePart(w http.ResponseWriter, flusher http.Flusher, frame []byte) bool {
	prelude := fmt.Sprintf("--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(frame))
	if _, err := w.Write([]byte(prelude)); err != nil {
		return false
	}
	if _, err := w.Write(frame); err != nil {
		return false
	}
	if _, err := w.Write([]byte("\r\n")); err != nil {
		return false
	}
	if flusher != nil {
		flusher.Flush()
	}
	return true
}

// parseCameraID extracts the camera_id from
// /preview/<camera_id>/stream. Returns (id, true) on a match. Anything
// else returns (_, false).
func parseCameraID(path string) (string, bool) {
	const prefix = "/preview/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	// expected: "<camera_id>/stream"
	if !strings.HasSuffix(rest, "/stream") {
		return "", false
	}
	id := strings.TrimSuffix(rest, "/stream")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// FFmpegRunner is the production RTSPRunner. Shells out to:
//
//	ffmpeg -rtsp_transport tcp -timeout 5000000 -i <url> \
//	       -f mjpeg -q:v 5 -r 10 -an pipe:1
//
// -timeout 5000000 caps the initial RTSP handshake at 5 seconds so
// the preview handler can fail over to 503 on an unreachable camera
// instead of hanging the request. Option-name history: old ffmpeg
// used -stimeout (deprecated in 6, removed in 7); briefly -rw_timeout
// was the documented replacement but it's not actually exposed by
// the ffmpeg 8 RTSP demuxer; -timeout (microseconds) is what works.
// Bench Mac runs 8.0.1; 2026-05-26 smoke pinned this via two
// "Option not found" rounds.
// exec.CommandContext kills ffmpeg when the request context is
// cancelled (client disconnect, server shutdown).
//
// The agent.AugmentSubprocessPath helper from cmd/uknomi-edge-ui/main
// ensures /opt/homebrew/bin (Apple Silicon) is on PATH so ffmpeg
// resolves under launchd's minimal default PATH.
type FFmpegRunner struct{}

// ffmpegArgs is the pure argv builder, extracted so a test pins the
// flag set against future ffmpeg releases (e.g. the -stimeout
// removal in ffmpeg 7).
func ffmpegArgs(rtspURL string) []string {
	return []string{
		"-rtsp_transport", "tcp",
		"-timeout", "5000000",
		"-i", rtspURL,
		"-f", "mjpeg",
		"-q:v", "5",
		"-r", "10",
		"-an",
		"pipe:1",
	}
}

// Start invokes ffmpeg and returns a FrameStream that splits ffmpeg's
// continuous JPEG-after-JPEG output on SOI markers. Cancelling ctx
// kills the subprocess via exec.CommandContext. Stderr is tailed
// into a bounded buffer; on first-frame failure the FrameStream's
// NextFrame returns an error containing the tail so the 503 surfaced
// to the operator includes ffmpeg's actual complaint instead of just
// "EOF".
func (FFmpegRunner) Start(ctx context.Context, rtspURL string) (FrameStream, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", ffmpegArgs(rtspURL)...)
	stderrTail := &tailBuf{max: 4096}
	cmd.Stderr = stderrTail
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &ffmpegFrames{stdout: stdout, cmd: cmd, stderrTail: stderrTail}, nil
}

// tailBuf keeps the last `max` bytes written to it; older bytes are
// discarded. Safe for the single-goroutine writer that exec.Cmd uses
// for cmd.Stderr — no mutex needed because we only read after the
// subprocess has exited.
type tailBuf struct {
	max int
	buf []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuf) String() string { return strings.TrimSpace(string(t.buf)) }

// ffmpegFrames splits ffmpeg's MJPEG-on-pipe output into discrete
// frames by scanning for the JPEG SOI marker (0xFFD8). A frame ends
// just before the next SOI (or at EOF).
type ffmpegFrames struct {
	stdout     io.ReadCloser
	cmd        *exec.Cmd
	buf        []byte
	stderrTail *tailBuf
	waited     bool
}

// NextFrame reads until two SOI markers have been seen (or EOF after
// the first) and returns the bytes between them. The first SOI is
// retained in the next frame's prefix.
func (f *ffmpegFrames) NextFrame() ([]byte, error) {
	// Ensure buffer starts at an SOI.
	for len(f.buf) < 2 || !(f.buf[0] == 0xFF && f.buf[1] == 0xD8) {
		if err := f.fill(); err != nil {
			return nil, f.wrapEOF(err)
		}
		// Drop bytes before the first SOI.
		if idx := findSOI(f.buf, 0); idx > 0 {
			f.buf = f.buf[idx:]
		}
	}
	// Find the next SOI starting after position 2.
	for {
		idx := findSOI(f.buf, 2)
		if idx >= 0 {
			frame := f.buf[:idx]
			f.buf = f.buf[idx:]
			out := make([]byte, len(frame))
			copy(out, frame)
			return out, nil
		}
		if err := f.fill(); err != nil {
			// EOF: emit the last partial frame if present.
			if len(f.buf) > 2 {
				out := make([]byte, len(f.buf))
				copy(out, f.buf)
				f.buf = nil
				return out, nil
			}
			// No frame produced. Wrap with stderr tail if any.
			return nil, f.wrapEOF(err)
		}
	}
}

// wrapEOF reaps the subprocess (so exec.Cmd's stderr-copy goroutine
// flushes into stderrTail) then appends the stderr tail to the error
// if ffmpeg said anything. Without the Wait, the read races the
// copy goroutine and stderrTail comes back empty — the original
// 2026-05-26 "just EOF, no ffmpeg complaint" bug surfaced this on
// both the "ffmpeg never produced a byte" path and the "ffmpeg
// produced a partial frame then exited" path; this helper closes
// both. Idempotent via f.waited.
func (f *ffmpegFrames) wrapEOF(err error) error {
	if !f.waited {
		_ = f.cmd.Wait()
		f.waited = true
	}
	if f.stderrTail != nil {
		if tail := f.stderrTail.String(); tail != "" {
			return fmt.Errorf("%w; ffmpeg: %s", err, tail)
		}
	}
	return err
}

func (f *ffmpegFrames) fill() error {
	chunk := make([]byte, 32*1024)
	n, err := f.stdout.Read(chunk)
	if n > 0 {
		f.buf = append(f.buf, chunk[:n]...)
	}
	if err != nil {
		return err
	}
	return nil
}

func findSOI(b []byte, from int) int {
	for i := from; i+1 < len(b); i++ {
		if b[i] == 0xFF && b[i+1] == 0xD8 {
			return i
		}
	}
	return -1
}

func (f *ffmpegFrames) Close() error {
	_ = f.stdout.Close()
	if !f.waited {
		_ = f.cmd.Wait()
		f.waited = true
	}
	return nil
}
