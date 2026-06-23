//go:build darwin

package agent

import (
	"context"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// bootInfoLogTimeout caps the one-shot `log show` read done at agent start so a
// slow log store never delays startup. The read happens once (cached), not per
// heartbeat.
const bootInfoLogTimeout = 10 * time.Second

// readBootInfo reads the system boot time (kern.boottime — no subprocess) and
// the previous-shutdown cause once at agent start. ok is always true on macOS;
// a failed shutdown-cause read still returns the boot time (HasShutdownCause
// false). The non-darwin build returns ok=false.
func readBootInfo() (BootInfo, bool) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return BootInfo{}, false
	}
	bootTime := time.Unix(int64(tv.Sec), int64(tv.Usec)*1000)
	info := BootInfo{BootTime: bootTime}

	if code, label, found := readShutdownCause(bootTime); found {
		info.ShutdownCauseCode = code
		info.ShutdownCause = label
		info.HasShutdownCause = true
	}
	return info, true
}

// readShutdownCause runs `log show` scoped to a short window around boot — the
// "Previous shutdown cause" line is written within seconds of boot, so scoping
// to that window keeps the read fast and correct no matter how long the system
// has been up (a wide --last window would miss it on a long-uptime device).
// Best-effort: any failure yields found=false and boot_time still flows.
func readShutdownCause(bootTime time.Time) (int, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), bootInfoLogTimeout)
	defer cancel()

	const layout = "2006-01-02 15:04:05"
	start := bootTime.Add(-1 * time.Minute).Format(layout)
	end := bootTime.Add(2 * time.Minute).Format(layout)

	out, err := exec.CommandContext(ctx, "log", "show",
		"--style", "syslog",
		"--start", start,
		"--end", end,
		"--predicate", `eventMessage CONTAINS "Previous shutdown cause"`,
	).Output()
	if err != nil {
		return 0, "", false
	}
	return parseShutdownCause(string(out))
}
