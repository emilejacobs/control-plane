package cameras_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	handler "github.com/emilejacobs/control-plane/internal/handlers/cameras"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// expectCodedError asserts err is an *envelope.CodedError with the
// given stable code. Mirrors the assertion pattern in configupdate's
// handler tests.
func expectCodedError(t *testing.T, err error, code string) {
	t.Helper()
	var coded *envelope.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error not *envelope.CodedError: %v (type %T)", err, err)
	}
	if coded.Code != code {
		t.Errorf("code: got %q want %q", coded.Code, code)
	}
}

// fakeApplier captures the call and returns either the input list
// (happy path) or an injected error.
type fakeApplier struct {
	calls  [][]cameras.Camera
	apply  func(list []cameras.Camera) ([]cameras.Camera, error)
}

func (f *fakeApplier) Apply(_ context.Context, list []cameras.Camera) ([]cameras.Camera, error) {
	f.calls = append(f.calls, list)
	if f.apply != nil {
		return f.apply(list)
	}
	return list, nil
}

func newHandler(t *testing.T, applier handler.Applier, fixedNow time.Time) *handler.Handler {
	t.Helper()
	h := handler.New(applier)
	// Inject a fixed clock for deterministic AppliedAt assertions.
	// We can't reach into the struct directly, so use the package's
	// "now" field via reflection isn't ideal — instead we just check
	// the format and let the test tolerate a recent timestamp.
	_ = fixedNow
	return h
}

// Happy path: payload with two cameras flows through Parse → Apply →
// Response with the AppliedAt stamp + EffectiveCameras equal to the
// input.
func TestHandleCamerasUpdateHappyPath(t *testing.T) {
	app := &fakeApplier{}
	h := newHandler(t, app, time.Now())

	raw := json.RawMessage(`{
		"cameras": [
			{"camera_id": "cam1", "label": "Drive-thru", "rtsp_url": "rtsp://a", "is_lpr": true},
			{"camera_id": "cam2", "label": "Entry",      "rtsp_url": "rtsp://b", "is_lpr": false}
		]
	}`)
	resp, err := h.Handle(context.Background(), raw)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	r, ok := resp.(handler.Response)
	if !ok {
		t.Fatalf("response type: got %T want handler.Response", resp)
	}
	if len(r.EffectiveCameras) != 2 {
		t.Errorf("effective cameras: got %d want 2", len(r.EffectiveCameras))
	}
	if r.AppliedAt == "" {
		t.Error("AppliedAt should not be empty")
	}
	if len(app.calls) != 1 {
		t.Fatalf("Applier.Apply calls: got %d want 1", len(app.calls))
	}
	if app.calls[0][0].CameraID != "cam1" {
		t.Errorf("first applier arg: got %+v", app.calls[0][0])
	}
}

// Empty cameras list is a valid payload — "this device has no
// cameras to track". Applier sees an empty slice (not nil) so its
// "write file" path doesn't need to special-case absent vs empty.
func TestHandleCamerasUpdateEmptyList(t *testing.T) {
	app := &fakeApplier{}
	h := newHandler(t, app, time.Now())

	resp, err := h.Handle(context.Background(), json.RawMessage(`{"cameras": []}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	r := resp.(handler.Response)
	if r.EffectiveCameras == nil {
		t.Error("EffectiveCameras should be non-nil (an empty slice)")
	}
	if len(r.EffectiveCameras) != 0 {
		t.Errorf("EffectiveCameras: got %d want 0", len(r.EffectiveCameras))
	}
	if len(app.calls) != 1 {
		t.Fatalf("Applier.Apply calls: got %d want 1", len(app.calls))
	}
	if app.calls[0] == nil {
		t.Error("Applier received nil; want empty slice")
	}
}

// Unknown top-level fields are rejected with a coded error — the
// agent does not silently absorb a field it doesn't recognise (ADR-028).
func TestHandleCamerasUpdateRejectsUnknownField(t *testing.T) {
	app := &fakeApplier{}
	h := newHandler(t, app, time.Now())

	raw := json.RawMessage(`{"cameras": [], "site_id": "extra"}`)
	_, err := h.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectCodedError(t, err, cameras.CodeUnknownField)
	if len(app.calls) != 0 {
		t.Errorf("Applier must not be called on validation failure; got %d", len(app.calls))
	}
}

// A camera with an empty label fails ValidateCamera; the agent
// returns a coded error and never calls Applier.
func TestHandleCamerasUpdateRejectsBadCamera(t *testing.T) {
	app := &fakeApplier{}
	h := newHandler(t, app, time.Now())

	raw := json.RawMessage(`{"cameras": [{"camera_id": "cam1", "label": "", "rtsp_url": "rtsp://a", "is_lpr": false}]}`)
	_, err := h.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectCodedError(t, err, cameras.CodeBadLabel)
	if len(app.calls) != 0 {
		t.Errorf("Applier must not be called on validation failure; got %d", len(app.calls))
	}
}

// If the Applier returns an error, the handler surfaces it as a
// coded error with the apply_failed code (not bad_payload, since the
// payload itself was fine).
func TestHandleCamerasUpdateApplierError(t *testing.T) {
	app := &fakeApplier{
		apply: func(list []cameras.Camera) ([]cameras.Camera, error) {
			return nil, errors.New("disk full")
		},
	}
	h := newHandler(t, app, time.Now())

	raw := json.RawMessage(`{"cameras": []}`)
	_, err := h.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error")
	}
	expectCodedError(t, err, "cameras.apply_failed")
}
