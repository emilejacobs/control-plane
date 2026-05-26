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

// Entry is the per-allow-list-row shape the agent's resolver consumes
// (ADR-030 § 5). Each entry carries a logical Name (what the dashboard
// surfaces), a Kind discriminator ("file" or "docker"), the Target the
// kind interprets (file path or container name), and a human Label.
// The Kind constants are public so the agent + handler can switch on
// them without re-deriving the string.
func TestEntryKindsExposed(t *testing.T) {
	if logtail.KindFile != "file" {
		t.Errorf("KindFile: got %q, want %q", logtail.KindFile, "file")
	}
	if logtail.KindDocker != "docker" {
		t.Errorf("KindDocker: got %q, want %q", logtail.KindDocker, "docker")
	}
}

// Entry struct holds all the fields the resolver needs in one place.
// Exercised here as a constructability check — the field set is the
// stable contract the agent + the dashboard both depend on.
func TestEntryStructShape(t *testing.T) {
	e := logtail.Entry{
		Name:   "plate-recognizer",
		Kind:   logtail.KindDocker,
		Target: "plate-recognizer-stream",
		Label:  "Plate Recognizer (Docker)",
	}
	if e.Name != "plate-recognizer" || e.Kind != "docker" || e.Target != "plate-recognizer-stream" {
		t.Errorf("Entry fields: %+v", e)
	}
}
