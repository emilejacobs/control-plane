package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// BootInfo is the device's once-per-boot offline-reason signal (PRD
// .scratch/offline-reason-tracking, #157): the system boot time and the
// previous-shutdown cause. CP compares a heartbeat's BootTime against the
// device's stored value — changed = a reboot (BootInfo says why), unchanged =
// a network/MQTT blip. Both fields are read once at agent start and cached;
// they're static for the life of the process.
type BootInfo struct {
	// BootTime is the system boot instant (kern.boottime). Stable across reads.
	BootTime time.Time
	// ShutdownCause is the mapped label for the previous shutdown's code; empty
	// when the cause couldn't be read (HasShutdownCause is then false).
	ShutdownCause string
	// ShutdownCauseCode is the raw integer code, preserved even when unmapped.
	ShutdownCauseCode int
	// HasShutdownCause distinguishes "no cause read" from code 0 (power loss).
	HasShutdownCause bool
}

// shutdownCauseLabels maps macOS "Previous shutdown cause" integer codes to
// human labels. 5/3/0 are the rock-solid, ubiquitously documented codes; the
// negative codes are the commonly-cited hardware/thermal/watchdog/panic causes
// and are best-effort — the mapping is refined against real device logs as we
// see them. Anything not here passes through verbatim via shutdownCauseLabel
// ("unknown (code N)"), so we never silently drop a code we haven't classified
// (Story 8).
var shutdownCauseLabels = map[int]string{
	5:    "clean restart",
	3:    "forced/hung",
	0:    "power loss",
	-62:  "watchdog timeout",
	-64:  "panic",
	-71:  "thermal",
	-86:  "thermal",
	-95:  "power fault",
	-128: "hardware fault",
}

// shutdownCauseLabel returns the mapped label for a code, or an
// "unknown (code N)" passthrough that preserves the raw value.
func shutdownCauseLabel(code int) string {
	if label, ok := shutdownCauseLabels[code]; ok {
		return label
	}
	return fmt.Sprintf("unknown (code %d)", code)
}

// shutdownCausePattern matches a "Previous shutdown cause: <int>" line in
// `log show` output. Case-insensitive; the code may be negative.
var shutdownCausePattern = regexp.MustCompile(`(?i)shutdown cause:?\s*(-?\d+)`)

// parseShutdownCause extracts the previous-shutdown code + mapped label from
// `log show` output. The most recent (last) matching line wins. found is false
// when no matching line is present — so callers can tell "no data" from a real
// code 0 (power loss).
func parseShutdownCause(logOutput string) (code int, label string, found bool) {
	matches := shutdownCausePattern.FindAllStringSubmatch(logOutput, -1)
	if len(matches) == 0 {
		return 0, "", false
	}
	last := matches[len(matches)-1]
	code, err := strconv.Atoi(last[1])
	if err != nil {
		return 0, "", false
	}
	return code, shutdownCauseLabel(code), true
}
