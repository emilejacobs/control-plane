package logtail_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

func TestParseHappyPath(t *testing.T) {
	req, err := logtail.Parse(json.RawMessage(`{"log_name":"agent","lines":200}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if req.LogName != "agent" || req.Lines != 200 {
		t.Errorf("got %+v", req)
	}
}

func TestParseRejectsBadLogName(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"empty", `{"log_name":"","lines":100}`},
		{"too long", `{"log_name":"` + strings.Repeat("x", 65) + `","lines":100}`},
		{"missing", `{"lines":100}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := logtail.Parse(json.RawMessage(tc.body))
			if err == nil {
				t.Fatal("expected error")
			}
			v, ok := logtail.AsValidation(err)
			if !ok || v.Code != logtail.CodeBadLogName {
				t.Errorf("error: got %v, want code %q", err, logtail.CodeBadLogName)
			}
		})
	}
}

func TestParseRejectsBadLines(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"zero", `{"log_name":"agent","lines":0}`},
		{"negative", `{"log_name":"agent","lines":-1}`},
		{"too many", `{"log_name":"agent","lines":501}`},
		{"missing", `{"log_name":"agent"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := logtail.Parse(json.RawMessage(tc.body))
			if err == nil {
				t.Fatal("expected error")
			}
			v, ok := logtail.AsValidation(err)
			if !ok || v.Code != logtail.CodeBadLines {
				t.Errorf("error: got %v, want code %q", err, logtail.CodeBadLines)
			}
		})
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := logtail.Parse(json.RawMessage(`{"log_name":"agent","lines":100,"evil_path":"/etc/passwd"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	v, ok := logtail.AsValidation(err)
	if !ok || v.Code != logtail.CodeUnknownField {
		t.Errorf("error: got %v, want code %q", err, logtail.CodeUnknownField)
	}
}

func TestAsValidationOnNonValidationError(t *testing.T) {
	_, ok := logtail.AsValidation(errors.New("plain error"))
	if ok {
		t.Error("AsValidation should reject non-ValidationError")
	}
}
