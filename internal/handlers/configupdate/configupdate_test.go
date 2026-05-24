package configupdate_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/handlers/configupdate"
)

// fakeApplier records the values it was invoked with and returns
// the configured effective state. err is returned to drive failure tests.
type fakeApplier struct {
	calls []applyCall
	retList     []string
	retInterval time.Duration
	err         error
}

type applyCall struct {
	allowList *[]string
	interval  *time.Duration
}

func (f *fakeApplier) Apply(_ context.Context, allowList *[]string, interval *time.Duration) ([]string, time.Duration, error) {
	f.calls = append(f.calls, applyCall{allowList: allowList, interval: interval})
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.retList, f.retInterval, nil
}

// Happy path: handler parses the payload, forwards pointers verbatim to
// the Applier, returns the effective state in its envelope-shaped
// response. Both fields present.
func TestHandlerHappyBothFields(t *testing.T) {
	applier := &fakeApplier{
		retList:     []string{"a", "b"},
		retInterval: 2 * time.Minute,
	}
	h := configupdate.New(applier)
	payload := json.RawMessage(`{"service_allow_list":["a","b"],"service_status_interval":"2m"}`)

	resp, err := h.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(applier.calls) != 1 {
		t.Fatalf("Apply calls: got %d, want 1", len(applier.calls))
	}
	c := applier.calls[0]
	if c.allowList == nil || (*c.allowList)[0] != "a" || (*c.allowList)[1] != "b" {
		t.Errorf("Apply allowList arg: got %v, want [a b]", c.allowList)
	}
	if c.interval == nil || *c.interval != 2*time.Minute {
		t.Errorf("Apply interval arg: got %v, want 2m", c.interval)
	}

	out, ok := resp.(configupdate.Response)
	if !ok {
		t.Fatalf("response type: got %T, want configupdate.Response", resp)
	}
	if len(out.EffectiveAllowList) != 2 || out.EffectiveAllowList[0] != "a" {
		t.Errorf("EffectiveAllowList: got %v, want [a b]", out.EffectiveAllowList)
	}
	if out.EffectiveInterval != "2m0s" {
		t.Errorf("EffectiveInterval: got %q, want %q", out.EffectiveInterval, "2m0s")
	}
	if out.AppliedAt == "" {
		t.Error("AppliedAt empty; expected RFC3339 timestamp")
	}
}

// nil pointer ⇒ "clear this override". Both nil arrives as both nil at
// the Applier. The handler must not turn nil into &[] (different
// semantics — empty list = "track nothing").
func TestHandlerNilForwardsAsClear(t *testing.T) {
	applier := &fakeApplier{retInterval: 5 * time.Minute}
	h := configupdate.New(applier)

	// Field present with explicit null AND field absent both clear.
	cases := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"service_allow_list":null,"service_status_interval":null}`),
	}
	for i, p := range cases {
		applier.calls = nil
		if _, err := h.Handle(context.Background(), p); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		c := applier.calls[0]
		if c.allowList != nil {
			t.Errorf("case %d allowList: got %v, want nil", i, c.allowList)
		}
		if c.interval != nil {
			t.Errorf("case %d interval: got %v, want nil", i, c.interval)
		}
	}
}

// Explicit [] is distinct from nil — "track nothing" not "clear override".
func TestHandlerEmptyListIsNotNil(t *testing.T) {
	applier := &fakeApplier{retList: []string{}}
	h := configupdate.New(applier)
	payload := json.RawMessage(`{"service_allow_list":[]}`)

	if _, err := h.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	c := applier.calls[0]
	if c.allowList == nil {
		t.Fatal("allowList: got nil, want &[]")
	}
	if len(*c.allowList) != 0 {
		t.Errorf("allowList: got %v, want []", *c.allowList)
	}
}

// Validation: interval must parse and sit within [30s, 1h]. Bad inputs
// return a CodedError so cp-side can surface the cause; the Applier is
// never called.
func TestHandlerRejectsBadInterval(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		code    string
	}{
		{"unparseable", `{"service_status_interval":"not-a-duration"}`, "config_update.bad_interval"},
		{"too short", `{"service_status_interval":"5s"}`, "config_update.bad_interval"},
		{"too long", `{"service_status_interval":"2h"}`, "config_update.bad_interval"},
		{"zero", `{"service_status_interval":"0s"}`, "config_update.bad_interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applier := &fakeApplier{}
			h := configupdate.New(applier)
			_, err := h.Handle(context.Background(), json.RawMessage(tc.payload))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var coded *envelope.CodedError
			if !errors.As(err, &coded) || coded.Code != tc.code {
				t.Errorf("error: got %v, want code %q", err, tc.code)
			}
			if len(applier.calls) != 0 {
				t.Errorf("Applier called %d times; should be 0 on validation failure", len(applier.calls))
			}
		})
	}
}

// Validation: every service name must be 1..256 chars. ADR-028 caps
// blast radius — the handler is a strict whitelist of two fields with
// bounded values.
func TestHandlerRejectsBadServiceName(t *testing.T) {
	long := make([]byte, 257)
	for i := range long {
		long[i] = 'x'
	}
	cases := []string{
		`{"service_allow_list":[""]}`,
		`{"service_allow_list":["` + string(long) + `"]}`,
	}
	for i, p := range cases {
		applier := &fakeApplier{}
		h := configupdate.New(applier)
		_, err := h.Handle(context.Background(), json.RawMessage(p))
		if err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
		var coded *envelope.CodedError
		if !errors.As(err, &coded) || coded.Code != "config_update.bad_service_name" {
			t.Errorf("case %d error: got %v, want code config_update.bad_service_name", i, err)
		}
	}
}

// Validation: unknown fields rejected. ADR-028 explicitly forbids
// scope creep beyond the two-field whitelist; an extra key in the
// payload is a strong signal something has drifted.
func TestHandlerRejectsUnknownField(t *testing.T) {
	applier := &fakeApplier{}
	h := configupdate.New(applier)
	_, err := h.Handle(context.Background(), json.RawMessage(`{"broker_url":"evil"}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var coded *envelope.CodedError
	if !errors.As(err, &coded) || coded.Code != "config_update.unknown_field" {
		t.Errorf("error: got %v, want code config_update.unknown_field", err)
	}
}
