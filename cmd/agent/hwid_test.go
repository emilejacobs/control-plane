package main

import "testing"

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
