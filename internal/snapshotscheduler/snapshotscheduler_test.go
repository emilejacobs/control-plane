package snapshotscheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	"github.com/emilejacobs/control-plane/internal/snapshotscheduler"
	"github.com/emilejacobs/control-plane/internal/snapshotstate"
)

type fakeCameras struct{ list []cameras.Camera }

func (f fakeCameras) Cameras(context.Context) ([]cameras.Camera, error) { return f.list, nil }

type fakeSnap struct {
	bytes []byte
	err   error
	rtsp  []string
}

func (f *fakeSnap) Snapshot(_ context.Context, rtsp string) ([]byte, error) {
	f.rtsp = append(f.rtsp, rtsp)
	return f.bytes, f.err
}

type upCall struct {
	kind     string
	metadata map[string]any
	size     int
}
type fakeUploader struct {
	mu    sync.Mutex
	calls []upCall
}

func (f *fakeUploader) Upload(_ context.Context, kind, _ string, data []byte, md map[string]any) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, upCall{kind: kind, metadata: md, size: len(data)})
	f.mu.Unlock()
	return "snapshots/dev/x.jpg", nil
}

// fakeState is an in-memory StateStore with a controllable cadence/schedule.
type fakeState struct {
	mu       sync.Mutex
	cadence  string
	nextFire map[string]time.Time
}

func (s *fakeState) Load() (snapshotstate.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := map[string]time.Time{}
	for k, v := range s.nextFire {
		cp[k] = v
	}
	return snapshotstate.State{Cadence: s.cadence, NextFire: cp}, nil
}

func (s *fakeState) SetNextFire(cameraID string, at time.Time) error {
	s.mu.Lock()
	if s.nextFire == nil {
		s.nextFire = map[string]time.Time{}
	}
	s.nextFire[cameraID] = at
	s.mu.Unlock()
	return nil
}

func twoCams() []cameras.Camera {
	return []cameras.Camera{
		{CameraID: "cam1", RtspURL: "rtsp://a/1"},
		{CameraID: "cam2", RtspURL: "rtsp://b/2"},
	}
}

func newSched(cam []cameras.Camera, snap *fakeSnap, up *fakeUploader, st *fakeState, now time.Time) *snapshotscheduler.Scheduler {
	return snapshotscheduler.New(fakeCameras{list: cam}, snap, up, st,
		snapshotscheduler.WithNow(func() time.Time { return now }))
}

func TestFiresNeverScheduledCameras(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	snap := &fakeSnap{bytes: []byte("jpeg")}
	up := &fakeUploader{}
	st := &fakeState{cadence: "weekly"}
	s := newSched(twoCams(), snap, up, st, now)

	s.CheckAndFire(context.Background())

	if len(up.calls) != 2 {
		t.Fatalf("uploads = %d, want 2 (both cameras first-sight)", len(up.calls))
	}
	if up.calls[0].kind != "snapshot" || up.calls[0].metadata["camera_id"] != "cam1" {
		t.Errorf("upload[0] = %+v", up.calls[0])
	}
	// Rescheduled a week out.
	want := now.Add(7 * 24 * time.Hour)
	if !st.nextFire["cam1"].Equal(want) {
		t.Errorf("cam1 next-fire = %v, want %v", st.nextFire["cam1"], want)
	}
}

func TestSkipsCamerasNotYetDue(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	st := &fakeState{cadence: "daily", nextFire: map[string]time.Time{
		"cam1": now.Add(2 * time.Hour),  // future → skip
		"cam2": now.Add(-time.Minute),   // past → fire
	}}
	up := &fakeUploader{}
	s := newSched(twoCams(), &fakeSnap{bytes: []byte("x")}, up, st, now)

	s.CheckAndFire(context.Background())

	if len(up.calls) != 1 || up.calls[0].metadata["camera_id"] != "cam2" {
		t.Fatalf("uploads = %+v, want only cam2", up.calls)
	}
	if !st.nextFire["cam1"].Equal(now.Add(2 * time.Hour)) {
		t.Error("cam1 next-fire should be unchanged")
	}
	if !st.nextFire["cam2"].Equal(now.Add(24 * time.Hour)) {
		t.Errorf("cam2 next-fire = %v, want +24h", st.nextFire["cam2"])
	}
}

func TestCadenceOffDoesNothing(t *testing.T) {
	now := time.Now()
	up := &fakeUploader{}
	s := newSched(twoCams(), &fakeSnap{bytes: []byte("x")}, up, &fakeState{cadence: "off"}, now)
	s.CheckAndFire(context.Background())
	if len(up.calls) != 0 {
		t.Errorf("cadence off should not upload, got %d", len(up.calls))
	}
}

// A capture failure still reschedules (no retry storm) and does not upload.
func TestCaptureFailureReschedulesWithoutUpload(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	up := &fakeUploader{}
	st := &fakeState{cadence: "weekly"}
	s := newSched([]cameras.Camera{{CameraID: "cam1", RtspURL: "rtsp://x"}},
		&fakeSnap{err: errors.New("ffmpeg boom")}, up, st, now)

	s.CheckAndFire(context.Background())

	if len(up.calls) != 0 {
		t.Errorf("failed capture should not upload, got %d", len(up.calls))
	}
	if !st.nextFire["cam1"].Equal(now.Add(7 * 24 * time.Hour)) {
		t.Errorf("cam1 should still be rescheduled, got %v", st.nextFire["cam1"])
	}
}

func TestCadenceIntervalMap(t *testing.T) {
	cases := map[string]time.Duration{
		"daily":  24 * time.Hour,
		"weekly": 7 * 24 * time.Hour,
		"off":    0,
		"":       0,
		"bogus":  0,
	}
	for c, want := range cases {
		if got := snapshotscheduler.CadenceInterval(c); got != want {
			t.Errorf("CadenceInterval(%q) = %v, want %v", c, got, want)
		}
	}
}
