package main

import "testing"

func TestParseOSReleaseVersion(t *testing.T) {
	// PRETTY_NAME wins when present.
	pretty := `NAME="Debian GNU/Linux"
PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
VERSION_ID="12"`
	if got := parseOSReleaseVersion(pretty); got != "Debian GNU/Linux 12 (bookworm)" {
		t.Errorf("PRETTY_NAME case = %q", got)
	}
	// Fall back to NAME + VERSION_ID when PRETTY_NAME is absent.
	noPretty := `NAME="Raspbian GNU/Linux"
VERSION_ID="11"`
	if got := parseOSReleaseVersion(noPretty); got != "Raspbian GNU/Linux 11" {
		t.Errorf("fallback case = %q", got)
	}
	// Nothing usable → empty.
	if got := parseOSReleaseVersion("# comment only\n"); got != "" {
		t.Errorf("empty case = %q", got)
	}
}

func TestLinuxKindFromModel(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"Raspberry Pi 4 Model B Rev 1.4", "pi"},
		{"Raspberry Pi 3 Model B Plus Rev 1.3", "pi"},
		{"Radxa ROCK 4C+", "radxa"},
		{"ROCK Pi 4B", "radxa"},
		{"Some Generic SBC", "linux"},
		{"", "linux"},
	}
	for _, c := range cases {
		if got := linuxKindFromModel(c.model); got != c.want {
			t.Errorf("linuxKindFromModel(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}
