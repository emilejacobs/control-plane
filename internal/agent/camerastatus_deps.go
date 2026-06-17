package agent

import (
	"context"
	"os/exec"
	"time"
)

// ffmpegReachability is the production telemetry.Reachability used by
// the camera-status probe (#113). It connects to the RTSP source, pulls
// a single video frame, and discards it to the null muxer — cheaper
// than the on-demand snapshot's mjpeg encode + pipe, but still proves
// the source is reachable AND delivering video rather than merely
// accepting a TCP connection.
//
// The -timeout (microseconds) caps the RTSP handshake so an unreachable
// camera fails fast instead of hanging the probe; see
// internal/edgeui/preview.go for the ffmpeg-8 flag-name history
// (-stimeout removed, -rw_timeout not exposed by the RTSP demuxer,
// -timeout is what works). exec.CommandContext kills ffmpeg when the
// per-probe deadline elapses or the agent shuts down.
type ffmpegReachability struct {
	timeout time.Duration
}

func newFFmpegReachability() *ffmpegReachability {
	return &ffmpegReachability{timeout: 10 * time.Second}
}

// Reachable reports whether ffmpeg could connect and decode one frame
// within the timeout. A non-nil exit (connect refused, timeout, auth
// failure, no video) counts as unreachable.
func (r *ffmpegReachability) Reachable(ctx context.Context, rtspURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin",
		"-rtsp_transport", "tcp",
		"-timeout", "5000000",
		"-i", rtspURL,
		"-frames:v", "1",
		"-f", "null",
		"-",
	)
	return cmd.Run() == nil
}
