package install_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/install"
)

// EnrollStep is done when the agent-config exists; Apply delegates to the
// injected enroll func (which, in production, writes that config).
func TestEnrollStep(t *testing.T) {
	fs := newFakeSystem()
	calls := 0
	step := &install.EnrollStep{
		Sys:        fs,
		ConfigPath: "/var/uknomi/agent-config.json",
		Enroll: func(context.Context) error {
			calls++
			fs.exists["/var/uknomi/agent-config.json"] = true
			return nil
		},
	}

	if done, _ := step.IsDone(context.Background()); done {
		t.Fatal("IsDone should be false before enrollment")
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if calls != 1 {
		t.Errorf("enroll called %d times, want 1", calls)
	}
	if done, _ := step.IsDone(context.Background()); !done {
		t.Error("IsDone should be true once the config exists")
	}
}

func samplePlan(fs *fakeSystem, enrollCalls *int) install.Plan {
	return install.Plan{
		AgentSrc:      "/pkg/uknomi-agent",
		AgentDst:      "/var/uknomi/agent-update/current",
		SupervisorSrc: "/pkg/uknomi-agent-supervisor",
		SupervisorDst: "/usr/local/bin/uknomi-agent-supervisor",
		ConfigPath:    "/var/uknomi/agent-config.json",
		Enroll: func(context.Context) error {
			*enrollCalls++
			fs.exists["/var/uknomi/agent-config.json"] = true
			return nil
		},
		Label:     "com.uknomi.agent",
		PlistPath: "/Library/LaunchDaemons/com.uknomi.agent.plist",
		Plist:     install.AgentLaunchDaemonPlist(install.AgentDaemonConfig{Label: "com.uknomi.agent"}),
	}
}

// A full Provision: BuildRunner wires binaries → enroll → launchdaemon, and the
// run installs everything. A second run is a complete no-op (every step IsDone),
// proving the assembled install is idempotent by inspection.
func TestBuildRunnerInstallsThenNoOps(t *testing.T) {
	fs := newFakeSystem()
	enrollCalls := 0
	plan := samplePlan(fs, &enrollCalls)

	if err := install.BuildRunner(fs, plan).Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if fs.copies["/var/uknomi/agent-update/current"] == "" {
		t.Error("agent binary not installed")
	}
	if enrollCalls != 1 {
		t.Errorf("enroll calls after first run: got %d want 1", enrollCalls)
	}
	if !fs.ran("launchctl load") {
		t.Error("daemon not loaded")
	}

	// Re-run: nothing should apply again.
	fs.runs = nil
	if err := install.BuildRunner(fs, plan).Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if enrollCalls != 1 {
		t.Errorf("enroll re-ran: got %d calls want 1 (idempotent)", enrollCalls)
	}
	if fs.ran("launchctl load") {
		t.Error("daemon reloaded on idempotent re-run")
	}
}
