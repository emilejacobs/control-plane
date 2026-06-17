package telemetry_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/protocol/camerastatus"
	"github.com/emilejacobs/control-plane/internal/telemetry"
)

// scriptReach is a fake Reachability whose answer per RTSP URL is read
// from a scripted sequence — one entry consumed per Collect call. When
// the script runs out it repeats the last value, so a test can set a
// steady state after a prefix of transitions.
type scriptReach struct {
	seq map[string][]bool
	idx map[string]int
}

func newScriptReach(seq map[string][]bool) *scriptReach {
	return &scriptReach{seq: seq, idx: map[string]int{}}
}

func (s *scriptReach) Reachable(_ context.Context, rtspURL string) bool {
	vals := s.seq[rtspURL]
	i := s.idx[rtspURL]
	s.idx[rtspURL]++
	switch {
	case i < len(vals):
		return vals[i]
	case len(vals) > 0:
		return vals[len(vals)-1]
	default:
		return false
	}
}

func camList(cams ...cameras.Camera) func(context.Context) ([]cameras.Camera, error) {
	return func(context.Context) ([]cameras.Camera, error) { return cams, nil }
}

// statusOf returns the reported status for cameraID in a Report, or ""
// if the camera was not reported this tick (undetermined).
func statusOf(r camerastatus.Report, cameraID string) string {
	for _, c := range r.Cameras {
		if c.CameraID == cameraID {
			return c.Status
		}
	}
	return ""
}

// A camera flips to offline only after Threshold consecutive failures;
// before that it is undetermined and not reported (so CP keeps it
// "unknown" rather than a premature offline).
func TestCameraCollectorDebouncesToOffline(t *testing.T) {
	cam := cameras.Camera{CameraID: "cam1", RtspURL: "rtsp://a"}
	reach := newScriptReach(map[string][]bool{"rtsp://a": {false, false, false}})
	c := &telemetry.CameraStatusCollector{
		DeviceID:  "dev-1",
		Cameras:   camList(cam),
		Reach:     reach,
		Threshold: 3,
	}

	// Tick 1, 2: still within the window — undetermined, not reported.
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != "" {
		t.Errorf("tick 1: got %q want undetermined (not reported)", got)
	}
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != "" {
		t.Errorf("tick 2: got %q want undetermined (not reported)", got)
	}
	// Tick 3: third consecutive failure flips to offline.
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != camerastatus.StatusOffline {
		t.Errorf("tick 3: got %q want offline", got)
	}
}

// A single transient miss inside the window does not flip an
// already-online camera; it stays online (no flap) and a subsequent
// success resets the failure count.
func TestCameraCollectorSingleMissDoesNotFlip(t *testing.T) {
	cam := cameras.Camera{CameraID: "cam1", RtspURL: "rtsp://a"}
	reach := newScriptReach(map[string][]bool{"rtsp://a": {true, false, true}})
	c := &telemetry.CameraStatusCollector{
		DeviceID: "dev-1", Cameras: camList(cam), Reach: reach, Threshold: 3,
	}

	if got := statusOf(c.Collect(context.Background()), "cam1"); got != camerastatus.StatusOnline {
		t.Fatalf("tick 1: got %q want online", got)
	}
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != camerastatus.StatusOnline {
		t.Errorf("tick 2 (single miss): got %q want online (no flap)", got)
	}
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != camerastatus.StatusOnline {
		t.Errorf("tick 3 (recovered): got %q want online", got)
	}
}

// Recovery is immediate: one success flips an offline camera back to
// online on the very next tick.
func TestCameraCollectorRecoversOnFirstSuccess(t *testing.T) {
	cam := cameras.Camera{CameraID: "cam1", RtspURL: "rtsp://a"}
	reach := newScriptReach(map[string][]bool{"rtsp://a": {false, false, true}})
	c := &telemetry.CameraStatusCollector{
		DeviceID: "dev-1", Cameras: camList(cam), Reach: reach, Threshold: 2,
	}

	_ = c.Collect(context.Background()) // fail 1
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != camerastatus.StatusOffline {
		t.Fatalf("tick 2: got %q want offline (threshold 2)", got)
	}
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != camerastatus.StatusOnline {
		t.Errorf("tick 3 (first success): got %q want online", got)
	}
}

// Per-camera independence: each camera debounces on its own; the report
// carries only determined cameras, each with its own status. The report
// is stamped with the device id and a correlation id.
func TestCameraCollectorPerCameraAndReportShape(t *testing.T) {
	online := cameras.Camera{CameraID: "cam1", RtspURL: "rtsp://up"}
	flapping := cameras.Camera{CameraID: "cam2", RtspURL: "rtsp://down"}
	reach := newScriptReach(map[string][]bool{
		"rtsp://up":   {true},
		"rtsp://down": {false}, // first failure only — undetermined at threshold 3
	})
	c := &telemetry.CameraStatusCollector{
		DeviceID: "dev-42", Cameras: camList(online, flapping), Reach: reach, Threshold: 3,
		Now: func() time.Time { return time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC) },
	}

	rep := c.Collect(context.Background())
	if rep.DeviceID != "dev-42" {
		t.Errorf("report device_id: got %q", rep.DeviceID)
	}
	if rep.CorrelationID == "" {
		t.Error("report correlation_id is empty")
	}
	if !rep.ReportedAt.Equal(time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("reported_at: got %v", rep.ReportedAt)
	}
	if got := statusOf(rep, "cam1"); got != camerastatus.StatusOnline {
		t.Errorf("cam1: got %q want online", got)
	}
	if got := statusOf(rep, "cam2"); got != "" {
		t.Errorf("cam2: got %q want undetermined (not reported)", got)
	}
	if len(rep.Cameras) != 1 {
		t.Errorf("report should carry only the 1 determined camera, got %d", len(rep.Cameras))
	}

	// Report must be valid JSON round-tripping the wire shape.
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back camerastatus.Report
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if statusOf(back, "cam1") != camerastatus.StatusOnline {
		t.Errorf("round-trip lost cam1 status")
	}
}

// A camera removed from the local list stops being tracked (its
// debounce state is pruned) and is no longer reported.
func TestCameraCollectorPrunesRemovedCameras(t *testing.T) {
	cam := cameras.Camera{CameraID: "cam1", RtspURL: "rtsp://a"}
	reach := newScriptReach(map[string][]bool{"rtsp://a": {true}})
	list := []cameras.Camera{cam}
	c := &telemetry.CameraStatusCollector{
		DeviceID: "dev-1",
		Cameras:  func(context.Context) ([]cameras.Camera, error) { return list, nil },
		Reach:    reach, Threshold: 3,
	}
	if got := statusOf(c.Collect(context.Background()), "cam1"); got != camerastatus.StatusOnline {
		t.Fatalf("tick 1: got %q want online", got)
	}
	// Remove the camera from the local list.
	list = nil
	rep := c.Collect(context.Background())
	if len(rep.Cameras) != 0 {
		t.Errorf("removed camera should not be reported, got %d cameras", len(rep.Cameras))
	}
}
