package main

import (
	"bufio"
	"strings"
)

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

// parseOSReleaseVersion turns /etc/os-release contents into a human OS label.
// Prefers PRETTY_NAME; falls back to "<NAME> <VERSION_ID>"; "" when neither.
func parseOSReleaseVersion(content string) string {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		kv[k] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	if p := kv["PRETTY_NAME"]; p != "" {
		return p
	}
	if n := kv["NAME"]; n != "" {
		if vid := kv["VERSION_ID"]; vid != "" {
			return n + " " + vid
		}
		return n
	}
	return ""
}
