package prconfigupdate_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/handlers/prconfigupdate"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

type fakeApplier struct {
	got prconfig.UpdateRequest
	err error
}

func (f *fakeApplier) Apply(_ context.Context, req prconfig.UpdateRequest) error {
	f.got = req
	return f.err
}

func TestHandleValid(t *testing.T) {
	fa := &fakeApplier{}
	h := prconfigupdate.New(fa)
	args := json.RawMessage(`{"camera_id":"66_3","region":"us-az","lpr_camera_rtsp_url":"rtsp://cam/0","webhooks":[{"name":"prod","url":"https://x.com/y","enabled":true,"image":true}]}`)
	res, err := h.Handle(context.Background(), args)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fa.got.CameraID != "66_3" || fa.got.LPRCameraRtspURL != "rtsp://cam/0" {
		t.Errorf("applier got %+v", fa.got)
	}
	if r, ok := res.(prconfigupdate.Response); !ok || !r.Restarted {
		t.Errorf("response = %+v", res)
	}
}

func TestHandleInvalidPayloadRejected(t *testing.T) {
	h := prconfigupdate.New(&fakeApplier{})
	// Bad region → validation error, applier never called.
	if _, err := h.Handle(context.Background(), json.RawMessage(`{"camera_id":"0","region":"BAD REGION"}`)); err == nil {
		t.Error("expected validation error for bad region")
	}
}

func TestHandleApplyError(t *testing.T) {
	h := prconfigupdate.New(&fakeApplier{err: errors.New("boom")})
	args := json.RawMessage(`{"camera_id":"0","region":"us-az"}`)
	if _, err := h.Handle(context.Background(), args); err == nil {
		t.Error("expected apply error to propagate")
	}
}
