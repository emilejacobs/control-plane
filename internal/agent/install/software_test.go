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

// EnsureFileStep copies a packaged file when the destination is absent, and is
// a no-op once present.
func TestEnsureFileStep(t *testing.T) {
	fs := newFakeSystem()
	step := &install.EnsureFileStep{
		Sys:      fs,
		StepName: "edge-ui-binary",
		Src:      "/pkg/uknomi-edge-ui",
		Dst:      "/usr/local/bin/uknomi-edge-ui",
		Mode:     0o755,
	}
	if step.Name() != "edge-ui-binary" {
		t.Errorf("Name: got %q", step.Name())
	}
	if done, _ := step.IsDone(context.Background()); done {
		t.Fatal("IsDone should be false before copy")
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if fs.copies["/usr/local/bin/uknomi-edge-ui"] != "/pkg/uknomi-edge-ui" {
		t.Errorf("edge-ui not copied: %v", fs.copies)
	}
	if done, _ := step.IsDone(context.Background()); !done {
		t.Error("IsDone should be true after copy")
	}
}

// WhisperModelStep downloads the model when absent (curl -o), and skips once
// the file is present.
func TestWhisperModelStep(t *testing.T) {
	fs := newFakeSystem()
	const dst = "/usr/local/etc/uknomi/whisper-models/ggml-medium.en-q5_0.bin"
	step := &install.WhisperModelStep{
		Sys: fs,
		URL: "https://example.com/ggml-medium.en-q5_0.bin",
		Dst: dst,
	}
	if done, _ := step.IsDone(context.Background()); done {
		t.Fatal("IsDone should be false before download")
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !fs.ran("curl") || !fs.ran(dst) || !fs.ran("https://example.com/ggml-medium.en-q5_0.bin") {
		t.Errorf("curl download not invoked correctly; runs=%v", fs.runs)
	}
	if done, _ := step.IsDone(context.Background()); !done {
		t.Error("IsDone should be true after download")
	}
}

// SoftwareSteps returns the uniform software steps in order, and a composed run
// installs everything; once the post-install state holds, a fresh run is a
// complete no-op (idempotent by inspection).
func TestSoftwareStepsComposeIdempotent(t *testing.T) {
	fs := newFakeSystem()
	cfg := install.SoftwareConfig{
		BrewUser:   "uknomi",
		BrewPath:   "/opt/homebrew/bin/brew",
		Formulae:   []string{"ffmpeg", "nmap"},
		EdgeUISrc:  "/pkg/uknomi-edge-ui",
		EdgeUIDst:  "/usr/local/bin/uknomi-edge-ui",
		WhisperURL: "https://example.com/model.bin",
		WhisperDst: "/usr/local/etc/uknomi/whisper-models/model.bin",
	}
	steps := install.SoftwareSteps(fs, cfg)

	wantNames := []string{"homebrew", "brew-formulae", "edge-ui-binary", "whisper-model"}
	if len(steps) != len(wantNames) {
		t.Fatalf("step count: got %d want %d", len(steps), len(wantNames))
	}
	for i, s := range steps {
		if s.Name() != wantNames[i] {
			t.Errorf("step %d: got %q want %q", i, s.Name(), wantNames[i])
		}
	}

	if err := install.NewRunner(steps...).Run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !fs.ran("install.sh") {
		t.Error("homebrew not installed")
	}
	if !fs.installed["ffmpeg"] || !fs.installed["nmap"] {
		t.Error("formulae not installed")
	}
	if fs.copies["/usr/local/bin/uknomi-edge-ui"] == "" {
		t.Error("edge-ui not copied")
	}
	if !fs.exists["/usr/local/etc/uknomi/whisper-models/model.bin"] {
		t.Error("whisper model not downloaded")
	}

	// Simulate the post-install state (brew now present) and re-run: no-op.
	fs.exists["/opt/homebrew/bin/brew"] = true
	fs.runs = nil
	installsBefore := fs.installCount
	if err := install.NewRunner(install.SoftwareSteps(fs, cfg)...).Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if fs.ran("install.sh") {
		t.Error("homebrew re-installed on idempotent re-run")
	}
	if fs.installCount != installsBefore {
		t.Errorf("formulae re-installed: %d new", fs.installCount-installsBefore)
	}
	if fs.ran("curl") {
		t.Error("whisper re-downloaded on idempotent re-run")
	}
}
