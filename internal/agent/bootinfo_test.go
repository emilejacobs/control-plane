package agent

import "testing"

// The shutdown-cause parser turns a representative `log show` line into an
// integer code + a mapped label. The known codes (5/3/0, a negative, and a
// kernel panic) map to their labels; an unmapped code passes through verbatim
// with its raw value preserved (Story 8). The brittle system reads sit behind
// a seam so this drives the parser with fixture strings — no device needed.
func TestParseShutdownCause(t *testing.T) {
	line := func(code string) string {
		return "2026-06-20 03:14:22.123456-0700  localhost kernel[0]: (AppleSMC) Previous shutdown cause: " + code
	}

	cases := []struct {
		name      string
		log       string
		wantCode  int
		wantLabel string
	}{
		{"clean restart", line("5"), 5, "clean restart"},
		{"forced/hung", line("3"), 3, "forced/hung"},
		{"power loss", line("0"), 0, "power loss"},
		{"thermal (negative)", line("-71"), -71, "thermal"},
		{"kernel panic", line("-64"), -64, "panic"},
		{"unmapped passthrough", line("-127"), -127, "unknown (code -127)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, label, found := parseShutdownCause(tc.log)
			if !found {
				t.Fatalf("parseShutdownCause(%q): found=false, want a match", tc.log)
			}
			if code != tc.wantCode {
				t.Errorf("code: got %d want %d", code, tc.wantCode)
			}
			if label != tc.wantLabel {
				t.Errorf("label: got %q want %q", label, tc.wantLabel)
			}
		})
	}
}

// With multiple matching lines (the predicate can surface more than one boot's
// worth), the most recent — last — line wins.
func TestParseShutdownCauseTakesLastMatch(t *testing.T) {
	log := "" +
		"2026-06-18 01:00:00.000000-0700  localhost kernel[0]: Previous shutdown cause: 3\n" +
		"2026-06-20 03:14:22.000000-0700  localhost kernel[0]: Previous shutdown cause: 5\n"
	code, label, found := parseShutdownCause(log)
	if !found || code != 5 || label != "clean restart" {
		t.Fatalf("got code=%d label=%q found=%v want 5/clean restart/true", code, label, found)
	}
}

// No matching line (the predicate returned nothing) reports not-found rather
// than a bogus zero code — distinguishing "no data" from "power loss" (code 0).
func TestParseShutdownCauseNotFound(t *testing.T) {
	if code, label, found := parseShutdownCause("nothing relevant here\n"); found {
		t.Errorf("got found=true (code=%d label=%q) want false", code, label)
	}
}
