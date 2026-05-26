package agent

import (
	"os"
	"strings"
)

// AugmentSubprocessPath appends macOS Homebrew install dirs to PATH so
// subprocess invocations (nmap for network.scan, docker for log.tail)
// resolve under launchd's minimal default PATH. Apple Silicon installs
// to /opt/homebrew/bin; Intel-era Homebrew uses /usr/local/bin. Both
// are well-known stable locations and safe to add on non-Mac OSes as
// well (the dirs simply won't exist there).
//
// Idempotent: existing entries are not duplicated.
func AugmentSubprocessPath() {
	extras := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	current := os.Getenv("PATH")
	parts := strings.Split(current, ":")
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		seen[p] = true
	}
	for _, e := range extras {
		if !seen[e] {
			parts = append(parts, e)
			seen[e] = true
		}
	}
	os.Setenv("PATH", strings.Join(parts, ":"))
}
