package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/protocol/camerastatus"
)

// defaultCameraThreshold is the number of consecutive reachability
// failures before a camera flips to offline (the debounce window).
// Recovery is immediate — a single success flips back to online.
const defaultCameraThreshold = 3

// Reachability is a cheap connect-level check that a camera's RTSP
// source is up. The production implementation shells a short ffmpeg
// probe (see camerastatus_deps.go in the agent package); tests inject a
// fake. It must not block longer than its own timeout.
type Reachability interface {
	Reachable(ctx context.Context, rtspURL string) bool
}

// CameraStatusCollector probes each configured camera's RTSP
// reachability once per Collect and returns a debounced Report (#113,
// PRD #111). Debounce is the deep part: a camera flips to offline only
// after Threshold consecutive failures, and back to online on the first
// success, so a single transient miss never alerts. A camera whose
// status is not yet determined (failing but still inside the window,
// never yet probed successfully) is omitted from the report, so CP
// keeps it "unknown" rather than reporting a premature offline.
//
// Collect holds debounce state across calls and is not safe for
// concurrent use — CameraStatusPublisher drives it from a single
// goroutine, like the other collectors.
type CameraStatusCollector struct {
	DeviceID  string
	Cameras   func(context.Context) ([]cameras.Camera, error)
	Reach     Reachability
	Threshold int // consecutive failures before offline; <=0 → default 3
	Now       func() time.Time
	Logger    *slog.Logger

	states map[string]*cameraDebounce
}

// cameraDebounce is the per-camera running state between ticks.
type cameraDebounce struct {
	failures   int
	status     string // last determined status (online/offline); "" until determined
	determined bool
}

// Collect probes every configured camera once and returns a stamped
// Report carrying only the cameras whose status is determined.
func (c *CameraStatusCollector) Collect(ctx context.Context) camerastatus.Report {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	log := c.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	threshold := c.Threshold
	if threshold <= 0 {
		threshold = defaultCameraThreshold
	}
	if c.states == nil {
		c.states = map[string]*cameraDebounce{}
	}

	report := camerastatus.Report{
		DeviceID:      c.DeviceID,
		CorrelationID: newCorrelationID(),
		ReportedAt:    now(),
	}

	cams, err := c.Cameras(ctx)
	if err != nil {
		// Can't read the local camera list this tick — report nothing
		// (the ingester upserts per camera, so an empty report is a
		// no-op) and keep the debounce state for the next tick.
		log.Error("camera-status: read cameras failed", "error", err)
		return report
	}

	seen := make(map[string]struct{}, len(cams))
	for _, cam := range cams {
		seen[cam.CameraID] = struct{}{}
		st := c.states[cam.CameraID]
		if st == nil {
			st = &cameraDebounce{}
			c.states[cam.CameraID] = st
		}
		if c.Reach.Reachable(ctx, cam.RtspURL) {
			st.failures = 0
			st.status = camerastatus.StatusOnline
			st.determined = true
		} else {
			st.failures++
			if st.failures >= threshold {
				st.status = camerastatus.StatusOffline
				st.determined = true
			}
			// Otherwise keep the prior determined status (no flap), or
			// stay undetermined if we've never established one.
		}
		if st.determined {
			report.Cameras = append(report.Cameras, camerastatus.CameraState{
				CameraID: cam.CameraID,
				Status:   st.status,
			})
		}
	}

	// Prune debounce state for cameras no longer in the local list so a
	// removed/renamed camera stops being tracked and reported.
	for id := range c.states {
		if _, ok := seen[id]; !ok {
			delete(c.states, id)
		}
	}

	return report
}

// CameraStatusPublisher drives a CameraStatusCollector on an Interval
// ticker and publishes each Report as JSON on
// devices/{DeviceID}/camera-status. Mirrors ProbePublisher /
// ServiceStatusPublisher.
type CameraStatusPublisher struct {
	Interval  time.Duration
	DeviceID  string
	Collect   func(context.Context) camerastatus.Report
	Transport Transport
	Logger    *slog.Logger

	mu     sync.Mutex
	ticker *time.Ticker
}

// SetInterval updates the cadence; if Run is active the ticker resets.
func (p *CameraStatusPublisher) SetInterval(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Interval = d
	if p.ticker != nil {
		p.ticker.Reset(d)
	}
}

// Run blocks until ctx is cancelled, publishing on every Interval tick.
func (p *CameraStatusPublisher) Run(ctx context.Context) {
	log := p.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	p.mu.Lock()
	p.ticker = time.NewTicker(p.Interval)
	ticker := p.ticker
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.ticker.Stop()
		p.ticker = nil
		p.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.publishOnce(ctx, log)
		}
	}
}

func (p *CameraStatusPublisher) publishOnce(ctx context.Context, log *slog.Logger) {
	report := p.Collect(ctx)
	body, err := json.Marshal(report)
	if err != nil {
		log.Error("camera-status marshal failed", "error", err, "correlation_id", report.CorrelationID)
		return
	}
	topic := "devices/" + p.DeviceID + "/camera-status"
	if err := p.Transport.Publish(topic, body); err != nil {
		log.Error("camera-status publish failed", "error", err, "correlation_id", report.CorrelationID, "topic", topic)
	}
}
