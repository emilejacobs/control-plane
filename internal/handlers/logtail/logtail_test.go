package logtail_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/handlers/logtail"
	protologtail "github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

type fakeReader struct {
	allow map[string]string
	calls []readCall
	resp  protologtail.Response
	err   error
}

type readCall struct {
	path  string
	lines int
}

func (f *fakeReader) AllowList() map[string]string { return f.allow }
func (f *fakeReader) Tail(path string, lines int) (protologtail.Response, error) {
	f.calls = append(f.calls, readCall{path: path, lines: lines})
	return f.resp, f.err
}

// Happy path: known log_name + valid lines → Reader.Tail called with
// the right path + lines, Response forwarded as the handler result.
func TestHandlerHappyPath(t *testing.T) {
	r := &fakeReader{
		allow: map[string]string{"agent": "/var/log/uknomi-agent.log"},
		resp:  protologtail.Response{Content: "line1\nline2\n", Truncated: false},
	}
	h := logtail.New(r)
	out, err := h.Handle(context.Background(), json.RawMessage(`{"log_name":"agent","lines":50}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("Tail calls: got %d, want 1", len(r.calls))
	}
	if r.calls[0].path != "/var/log/uknomi-agent.log" || r.calls[0].lines != 50 {
		t.Errorf("Tail args: got %+v", r.calls[0])
	}
	resp, ok := out.(protologtail.Response)
	if !ok {
		t.Fatalf("output type: got %T, want logtail.Response", out)
	}
	if resp.Content != "line1\nline2\n" {
		t.Errorf("content: got %q", resp.Content)
	}
}

// Unknown log_name: handler returns CodeUnknownLog, Reader.Tail is
// never called. This is the security boundary — the agent refuses to
// read paths outside its allow-list even if the cmd payload validates.
func TestHandlerUnknownLog(t *testing.T) {
	r := &fakeReader{allow: map[string]string{"agent": "/var/log/uknomi-agent.log"}}
	h := logtail.New(r)
	_, err := h.Handle(context.Background(), json.RawMessage(`{"log_name":"evil","lines":50}`))
	if err == nil {
		t.Fatal("expected error")
	}
	var coded *envelope.CodedError
	if !errors.As(err, &coded) || coded.Code != protologtail.CodeUnknownLog {
		t.Errorf("error: got %v, want code %q", err, protologtail.CodeUnknownLog)
	}
	if len(r.calls) != 0 {
		t.Errorf("Tail should not be called on unknown log; got %d calls", len(r.calls))
	}
}

// Validation errors from Parse propagate as CodedError with the
// protocol-side code preserved.
func TestHandlerValidationFailureForwarded(t *testing.T) {
	r := &fakeReader{allow: map[string]string{"agent": "/var/log/uknomi-agent.log"}}
	h := logtail.New(r)
	_, err := h.Handle(context.Background(), json.RawMessage(`{"log_name":"agent","lines":1000}`))
	if err == nil {
		t.Fatal("expected error")
	}
	var coded *envelope.CodedError
	if !errors.As(err, &coded) || coded.Code != protologtail.CodeBadLines {
		t.Errorf("error: got %v, want code %q", err, protologtail.CodeBadLines)
	}
}

// Reader errors (CodeBinaryFile, CodeReadError) propagate with their
// original code so the cmd-result envelope carries the agent-side
// reason intact.
func TestHandlerReaderErrorPropagated(t *testing.T) {
	r := &fakeReader{
		allow: map[string]string{"install": "/var/log/install.log"},
		err: &protologtail.ValidationError{
			Code:    protologtail.CodeBinaryFile,
			Message: "looks binary",
		},
	}
	h := logtail.New(r)
	_, err := h.Handle(context.Background(), json.RawMessage(`{"log_name":"install","lines":50}`))
	if err == nil {
		t.Fatal("expected error")
	}
	var coded *envelope.CodedError
	if !errors.As(err, &coded) || coded.Code != protologtail.CodeBinaryFile {
		t.Errorf("error: got %v, want code %q", err, protologtail.CodeBinaryFile)
	}
}
