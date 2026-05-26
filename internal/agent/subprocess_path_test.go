package agent

import (
	"os"
	"testing"
)

// LaunchDaemon's default PATH on macOS does not include Homebrew dirs;
// `nmap` and `docker` both live under /opt/homebrew/bin on Apple
// Silicon. AugmentSubprocessPath must extend PATH so subprocess
// invocations succeed without touching the plist.
func TestAugmentSubprocessPath(t *testing.T) {
	orig := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", orig) })

	cases := []struct {
		name, before, want string
	}{
		{
			"launchd-minimal PATH gains homebrew dirs",
			"/usr/bin:/bin:/usr/sbin:/sbin",
			"/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin",
		},
		{
			"idempotent when both dirs already present",
			"/opt/homebrew/bin:/usr/local/bin:/usr/bin",
			"/opt/homebrew/bin:/usr/local/bin:/usr/bin",
		},
		{
			"adds only the missing one",
			"/opt/homebrew/bin:/usr/bin",
			"/opt/homebrew/bin:/usr/bin:/usr/local/bin",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Setenv("PATH", c.before)
			AugmentSubprocessPath()
			if got := os.Getenv("PATH"); got != c.want {
				t.Errorf("PATH:\n got %q\nwant %q", got, c.want)
			}
		})
	}
}
