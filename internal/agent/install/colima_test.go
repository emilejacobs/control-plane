package install_test

import (
	"context"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/install"
)

// The Colima LaunchAgent starts the per-user VM at login with the vz backend
// and the stream-dir mount, so the container's config.ini round-trips to the
// host (ADR-038; validated by plate-recognizer-colima-test.sh).
func TestColimaLaunchAgentPlistContents(t *testing.T) {
	plist := string(install.ColimaLaunchAgentPlist(install.ColimaAgentConfig{
		Label:      "com.uknomi.colima",
		ColimaPath: "/opt/homebrew/bin/colima",
		CPU:        2,
		MemoryGiB:  4,
		DiskGiB:    30,
		MountDir:   "/usr/local/etc/plate-recognizer/stream",
		StdoutPath: "/tmp/colima.log",
		StderrPath: "/tmp/colima-error.log",
	}))
	for _, want := range []string{
		"com.uknomi.colima",
		"/opt/homebrew/bin/colima",
		"start",
		"--vm-type",
		"vz",
		// LAN reachability: the VZNAT reachable network becomes the VM's default
		// route so the container can reach the directly-connected RTSP camera
		// (ADR-038; without preferred-route the lima usernet default wins).
		"--network-address",
		"--network-preferred-route",
		"--mount",
		"/usr/local/etc/plate-recognizer/stream:w",
		"RunAtLoad",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q\n%s", want, plist)
		}
	}
}

// ColimaVMSize sizes the VM to the host: ALPR inference is CPU-bound and Docker
// Desktop gave it ~all cores, so a fixed 2 vCPUs tanks recognition health. Leave
// 2 cores for macOS; ~half the RAM capped 4–8 GiB. Mirrors migrate-colima.sh.
func TestColimaVMSize(t *testing.T) {
	cases := []struct {
		numCPU, memGiB           int
		wantCPU, wantMem, wantDk int
	}{
		{10, 16, 8, 8, 30},  // M4 mini
		{8, 8, 6, 4, 30},    // 8-core / 8 GiB
		{4, 8, 2, 4, 30},    // small host: keep 2 vCPUs minimum
		{2, 4, 2, 4, 30},    // tiny: cpu floor 2, mem floor 4
		{16, 64, 14, 8, 30}, // big host: mem capped at 8
	}
	for _, c := range cases {
		cpu, mem, dk := install.ColimaVMSize(c.numCPU, c.memGiB)
		if cpu != c.wantCPU || mem != c.wantMem || dk != c.wantDk {
			t.Errorf("ColimaVMSize(%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
				c.numCPU, c.memGiB, cpu, mem, dk, c.wantCPU, c.wantMem, c.wantDk)
		}
	}
}

// ColimaLaunchAgentStep writes the user LaunchAgent when absent, no-op once present.
func TestColimaLaunchAgentStep(t *testing.T) {
	fs := newFakeSystem()
	step := &install.ColimaLaunchAgentStep{
		Sys:   fs,
		Path:  "/Users/uknomi/Library/LaunchAgents/com.uknomi.colima.plist",
		Plist: []byte("<plist/>"),
	}
	if done, _ := step.IsDone(context.Background()); done {
		t.Fatal("IsDone should be false before write")
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := fs.wrote["/Users/uknomi/Library/LaunchAgents/com.uknomi.colima.plist"]; !ok {
		t.Error("LaunchAgent plist not written")
	}
	if done, _ := step.IsDone(context.Background()); !done {
		t.Error("IsDone should be true after write")
	}
}

// ColimaSteps installs colima + docker (as the brew user) and writes the
// LaunchAgent; it does NOT start the container (that awaits Commission). A
// re-run is a no-op.
func TestColimaStepsComposeIdempotent(t *testing.T) {
	fs := newFakeSystem()
	cfg := install.ColimaConfig{
		BrewUser:         "uknomi",
		BrewPath:         "/opt/homebrew/bin/brew",
		LaunchAgentPath:  "/Users/uknomi/Library/LaunchAgents/com.uknomi.colima.plist",
		LaunchAgentPlist: []byte("<plist/>"),
	}
	steps := install.ColimaSteps(fs, cfg)
	wantNames := []string{"brew-formulae", "colima-launchagent"}
	if len(steps) != len(wantNames) {
		t.Fatalf("steps: got %d want %d", len(steps), len(wantNames))
	}
	for i, s := range steps {
		if s.Name() != wantNames[i] {
			t.Errorf("step %d: got %q want %q", i, s.Name(), wantNames[i])
		}
	}

	if err := install.NewRunner(steps...).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !fs.installed["colima"] || !fs.installed["docker"] {
		t.Error("colima/docker not installed")
	}
	if _, ok := fs.wrote[cfg.LaunchAgentPath]; !ok {
		t.Error("LaunchAgent not written")
	}
	// The container must not be started during install.
	if fs.ran("docker run") {
		t.Error("container started during install — must await Commission")
	}

	fs.runs = nil
	installsBefore := fs.installCount
	if err := install.NewRunner(install.ColimaSteps(fs, cfg)...).Run(context.Background()); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if fs.installCount != installsBefore {
		t.Errorf("colima/docker re-installed on idempotent re-run")
	}
}
