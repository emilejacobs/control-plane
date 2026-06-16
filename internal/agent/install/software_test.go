package install_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/install"
)

// Homebrew is installed (as the non-root brew user) only when no brew binary is
// present; once present the step is a no-op.
func TestHomebrewStep(t *testing.T) {
	fs := newFakeSystem()
	step := &install.HomebrewStep{Sys: fs, User: "uknomi"}

	if done, _ := step.IsDone(context.Background()); done {
		t.Fatal("IsDone should be false with no brew present")
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !fs.ran("install.sh") {
		t.Errorf("Homebrew install script not run; runs=%v", fs.runs)
	}
	if !fs.ran("sudo -u uknomi") {
		t.Errorf("Homebrew install should run as the brew user, not root; runs=%v", fs.runs)
	}

	// Simulate brew now present.
	fs.exists["/opt/homebrew/bin/brew"] = true
	if done, _ := step.IsDone(context.Background()); !done {
		t.Error("IsDone should be true once brew exists")
	}
}

// Formulae are installed when missing and skipped when present; a re-run after
// everything is installed performs no further installs.
func TestBrewFormulaeStep(t *testing.T) {
	fs := newFakeSystem()
	step := &install.BrewFormulaeStep{
		Sys:      fs,
		User:     "uknomi",
		BrewPath: "/opt/homebrew/bin/brew",
		Formulae: []string{"ffmpeg", "tailscale", "nmap", "whisper-cpp"},
	}

	if done, _ := step.IsDone(context.Background()); done {
		t.Fatal("IsDone should be false with nothing installed")
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, f := range []string{"ffmpeg", "tailscale", "nmap", "whisper-cpp"} {
		if !fs.installed[f] {
			t.Errorf("%s not installed", f)
		}
	}
	if done, _ := step.IsDone(context.Background()); !done {
		t.Error("IsDone should be true once all formulae present")
	}

	// One formula already present: Apply installs only the missing ones.
	fs2 := newFakeSystem()
	fs2.installed["ffmpeg"] = true
	step2 := &install.BrewFormulaeStep{Sys: fs2, User: "uknomi", BrewPath: "/opt/homebrew/bin/brew",
		Formulae: []string{"ffmpeg", "nmap"}}
	if err := step2.Apply(context.Background()); err != nil {
		t.Fatalf("Apply (partial): %v", err)
	}
	if fs2.installCount != 1 {
		t.Errorf("installs run: got %d want 1 (only the missing nmap)", fs2.installCount)
	}
}
