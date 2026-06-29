package main

import "strings"

// Linux host-identity helpers for `agent enroll` on the legacy Pi/Radxa fleet
// (ADR-007). Pure parsers live here (testable on any OS); the file reads that
// feed them are in enroll_linux.go. macOS identity stays in enroll_darwin.go.

// linuxKindFromModel maps a device-tree model string to a CP hardware_kind.
// Raspberry Pi → "pi", Radxa/ROCK boards → "radxa", anything else → "linux".
func linuxKindFromModel(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "raspberry"):
		return "pi"
	case strings.Contains(m, "radxa"), strings.Contains(m, "rock"):
		return "radxa"
	default:
		return "linux"
	}
}
