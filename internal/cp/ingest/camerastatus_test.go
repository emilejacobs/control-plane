package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/camerastatus"
)

type cameraStatusWriteCall struct {
	deviceID  string
	cameraID  string
	status    string
	checkedAt time.Time
}

// fakeCameraStatusWriter records every UpdateCameraStatus call and
// returns a per-camera error (errByCamera) or a default error.
type fakeCameraStatusWriter struct {
	errByCamera map[string]error
	defaultErr  error
	calls       []cameraStatusWriteCall
}

func (f *fakeCameraStatusWriter) UpdateCameraStatus(_ context.Context, deviceID, cameraID, status string, checkedAt time.Time) error {
	f.calls = append(f.calls, cameraStatusWriteCall{deviceID, cameraID, status, checkedAt})
	if e, ok := f.errByCamera[cameraID]; ok {
		return e
	}
	return f.defaultErr
}

// Happy path: a report with two cameras and a known device → writer
// called once per camera with the right ids/status and the cp-side
// ingest timestamp (not the agent's ReportedAt, since agent clocks
// drift).
func TestCameraStatusIngesterHappyPath(t *testing.T) {
	ingestAt := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	agentAt := time.Date(2026, 6, 17, 8, 59, 0, 0, time.UTC)
	w := &fakeCameraStatusWriter{}
	ing := NewCameraStatusIngester(w, func() time.Time { return ingestAt })

	err := ing.Handle(context.Background(), camerastatus.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		ReportedAt:    agentAt,
		Cameras: []camerastatus.CameraState{
			{CameraID: "cam1", Status: camerastatus.StatusOffline},
			{CameraID: "cam2", Status: camerastatus.StatusOnline},
		},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.calls) != 2 {
		t.Fatalf("writer calls: got %d want 2", len(w.calls))
	}
	if w.calls[0] != (cameraStatusWriteCall{"dev-1", "cam1", camerastatus.StatusOffline, ingestAt}) {
		t.Errorf("call 0: got %+v", w.calls[0])
	}
	if w.calls[1] != (cameraStatusWriteCall{"dev-1", "cam2", camerastatus.StatusOnline, ingestAt}) {
		t.Errorf("call 1: got %+v", w.calls[1])
	}
}

// A report with no device_id is poison — it can never succeed on retry,
// so it goes straight to the DLQ and the writer is never called.
func TestCameraStatusIngesterEmptyDeviceIsPoison(t *testing.T) {
	w := &fakeCameraStatusWriter{}
	ing := NewCameraStatusIngester(w, nil)

	err := ing.Handle(context.Background(), camerastatus.Report{
		Cameras: []camerastatus.CameraState{{CameraID: "cam1", Status: camerastatus.StatusOnline}},
	})
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("empty device_id: got %v want poison", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("writer should not be called, got %d calls", len(w.calls))
	}
}

// A camera the CP doesn't know (ErrCameraNotFound — removed in CP, or a
// device decommissioned, or probed before the import) is skipped, not
// poison: the report's other cameras still apply and Handle succeeds so
// the message is deleted rather than retried forever.
func TestCameraStatusIngesterUnknownCameraSkipped(t *testing.T) {
	w := &fakeCameraStatusWriter{errByCamera: map[string]error{"cam-gone": registry.ErrCameraNotFound}}
	ing := NewCameraStatusIngester(w, nil)

	err := ing.Handle(context.Background(), camerastatus.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		Cameras: []camerastatus.CameraState{
			{CameraID: "cam-gone", Status: camerastatus.StatusOffline},
			{CameraID: "cam2", Status: camerastatus.StatusOnline},
		},
	})
	if err != nil {
		t.Fatalf("Handle should swallow ErrCameraNotFound, got %v", err)
	}
	// Both cameras were attempted; the second still applied.
	if len(w.calls) != 2 {
		t.Fatalf("writer calls: got %d want 2 (both attempted)", len(w.calls))
	}
}

// A transient writer error (not ErrCameraNotFound) propagates unchanged
// so SQS redelivers after the visibility timeout.
func TestCameraStatusIngesterTransientErrorRedelivers(t *testing.T) {
	boom := errors.New("db unavailable")
	w := &fakeCameraStatusWriter{defaultErr: boom}
	ing := NewCameraStatusIngester(w, nil)

	err := ing.Handle(context.Background(), camerastatus.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		Cameras:       []camerastatus.CameraState{{CameraID: "cam1", Status: camerastatus.StatusOnline}},
	})
	if !errors.Is(err, boom) {
		t.Errorf("transient error: got %v want %v", err, boom)
	}
	if errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("transient error must not be poison")
	}
}

// A camera entry with an empty camera_id is malformed and skipped — no
// writer call for it.
func TestCameraStatusIngesterSkipsEmptyCameraID(t *testing.T) {
	w := &fakeCameraStatusWriter{}
	ing := NewCameraStatusIngester(w, nil)

	err := ing.Handle(context.Background(), camerastatus.Report{
		DeviceID:      "dev-1",
		CorrelationID: "corr-1",
		Cameras: []camerastatus.CameraState{
			{CameraID: "", Status: camerastatus.StatusOnline},
			{CameraID: "cam2", Status: camerastatus.StatusOnline},
		},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.calls) != 1 || w.calls[0].cameraID != "cam2" {
		t.Errorf("expected only cam2 written, got %+v", w.calls)
	}
}
