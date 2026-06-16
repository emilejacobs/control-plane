package commission_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	handler "github.com/emilejacobs/control-plane/internal/handlers/commission"
)

type fakeApplier struct {
	joinedKey  string
	joinErr    error
	alprLic    string
	alprTok    string
	alprErr    error
	alprCalled bool
}

func (f *fakeApplier) JoinTailnet(_ context.Context, authKey string) error {
	f.joinedKey = authKey
	return f.joinErr
}
func (f *fakeApplier) StartALPR(_ context.Context, license, token string) error {
	f.alprCalled = true
	f.alprLic, f.alprTok = license, token
	return f.alprErr
}

func dispatch(h *handler.Handler, raw string) (any, error) {
	return h.Handle(context.Background(), json.RawMessage(raw))
}

// Tailscale-only commission: join the tailnet, leave ALPR untouched.
func TestHandleTailscaleOnly(t *testing.T) {
	fa := &fakeApplier{}
	h := handler.New(fa)

	res, err := dispatch(h, `{"tailscale_auth_key":"tskey-x"}`)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fa.joinedKey != "tskey-x" {
		t.Errorf("join key: got %q", fa.joinedKey)
	}
	if fa.alprCalled {
		t.Error("StartALPR should not be called without an ALPR block")
	}
	resp := res.(handler.Response)
	if !resp.TailscaleJoined || resp.ALPRStarted {
		t.Errorf("response: %+v", resp)
	}
}

// ALPR commission: join + start the container with license/token.
func TestHandleWithALPR(t *testing.T) {
	fa := &fakeApplier{}
	res, err := dispatch(handler.New(fa), `{"tailscale_auth_key":"k","alpr":{"license":"LIC","token":"TOK"}}`)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !fa.alprCalled || fa.alprLic != "LIC" || fa.alprTok != "TOK" {
		t.Errorf("StartALPR not called correctly: %+v", fa)
	}
	if !res.(handler.Response).ALPRStarted {
		t.Error("ALPRStarted should be true")
	}
}

func TestHandleBadPayload(t *testing.T) {
	if _, err := dispatch(handler.New(&fakeApplier{}), `{"bogus":true}`); err == nil {
		t.Fatal("expected coded error on bad payload")
	}
}

func TestHandleTailscaleFailure(t *testing.T) {
	fa := &fakeApplier{joinErr: errors.New("tailscale up failed")}
	if _, err := dispatch(handler.New(fa), `{"tailscale_auth_key":"k"}`); err == nil {
		t.Fatal("expected error when JoinTailnet fails")
	}
}

func TestHandleALPRFailure(t *testing.T) {
	fa := &fakeApplier{alprErr: errors.New("docker run failed")}
	_, err := dispatch(handler.New(fa), `{"tailscale_auth_key":"k","alpr":{"license":"L","token":"T"}}`)
	if err == nil {
		t.Fatal("expected error when StartALPR fails")
	}
}
