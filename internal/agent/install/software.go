package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultHomebrewInstall is the non-interactive Homebrew bootstrap command. Run
// as the brew user (never root — Homebrew refuses to run as root).
const DefaultHomebrewInstall = `NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`

// DefaultBrewPaths are the Apple-Silicon and Intel Homebrew prefixes.
var DefaultBrewPaths = []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"}

// HomebrewStep installs Homebrew if no brew binary is present. Idempotent by
// inspection (a brew binary at either prefix). Apply runs the bootstrap as the
// non-root brew User.
type HomebrewStep struct {
	Sys        System
	User       string   // brew user, e.g. "uknomi"
	BrewPaths  []string // candidate brew binaries; nil → DefaultBrewPaths
	InstallCmd string   // bootstrap command; "" → DefaultHomebrewInstall
}

func (s *HomebrewStep) Name() string { return "homebrew" }

func (s *HomebrewStep) IsDone(_ context.Context) (bool, error) {
	for _, p := range s.brewPaths() {
		ok, err := s.Sys.Exists(p)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (s *HomebrewStep) Apply(ctx context.Context) error {
	cmd := s.InstallCmd
	if cmd == "" {
		cmd = DefaultHomebrewInstall
	}
	if err := s.Sys.Run(ctx, "sudo", "-u", s.User, "bash", "-c", cmd); err != nil {
		return fmt.Errorf("install homebrew: %w", err)
	}
	return nil
}

func (s *HomebrewStep) brewPaths() []string {
	if len(s.BrewPaths) > 0 {
		return s.BrewPaths
	}
	return DefaultBrewPaths
}

// BrewFormulaeStep installs the uniform formula set (ffmpeg, tailscale, nmap,
// whisper-cpp — colima + docker CLI land in the Colima slice, #89). Idempotent
// by inspection: done when every formula is present (`brew list`); Apply
// installs only the missing ones. All brew commands run as the non-root User.
type BrewFormulaeStep struct {
	Sys      System
	User     string
	BrewPath string // resolved brew binary
	Formulae []string
}

func (s *BrewFormulaeStep) Name() string { return "brew-formulae" }

func (s *BrewFormulaeStep) IsDone(ctx context.Context) (bool, error) {
	for _, f := range s.Formulae {
		if !s.present(ctx, f) {
			return false, nil
		}
	}
	return true, nil
}

func (s *BrewFormulaeStep) Apply(ctx context.Context) error {
	for _, f := range s.Formulae {
		if s.present(ctx, f) {
			continue
		}
		if err := s.Sys.Run(ctx, "sudo", "-u", s.User, s.BrewPath, "install", f); err != nil {
			return fmt.Errorf("brew install %s: %w", f, err)
		}
	}
	return nil
}

// present reports whether a formula is already installed. `brew list` exits
// non-zero when it isn't.
func (s *BrewFormulaeStep) present(ctx context.Context, formula string) bool {
	return s.Sys.Run(ctx, "sudo", "-u", s.User, s.BrewPath, "list", "--formula", formula) == nil
}

// EnsureFileStep copies a packaged file to a destination when absent — used for
// the edge-ui binary (the agent + supervisor have their own InstallBinariesStep).
// Idempotent by inspection (destination exists).
type EnsureFileStep struct {
	Sys      System
	StepName string
	Src      string
	Dst      string
	Mode     os.FileMode
}

func (s *EnsureFileStep) Name() string { return s.StepName }

func (s *EnsureFileStep) IsDone(_ context.Context) (bool, error) {
	return s.Sys.Exists(s.Dst)
}

func (s *EnsureFileStep) Apply(_ context.Context) error {
	if err := s.Sys.MkdirAll(filepath.Dir(s.Dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(s.Dst), err)
	}
	if err := s.Sys.CopyFile(s.Src, s.Dst, s.Mode); err != nil {
		return fmt.Errorf("copy %s: %w", s.StepName, err)
	}
	return nil
}

// WhisperModelStep downloads the Whisper model when absent. Bundled on every
// device (disk only) so #10 audio QA has it; idempotent by inspection.
type WhisperModelStep struct {
	Sys System
	URL string
	Dst string // e.g. /usr/local/etc/uknomi/whisper-models/ggml-medium.en-q5_0.bin
}

func (s *WhisperModelStep) Name() string { return "whisper-model" }

func (s *WhisperModelStep) IsDone(_ context.Context) (bool, error) {
	return s.Sys.Exists(s.Dst)
}

func (s *WhisperModelStep) Apply(ctx context.Context) error {
	if err := s.Sys.MkdirAll(filepath.Dir(s.Dst), 0o755); err != nil {
		return fmt.Errorf("mkdir whisper model dir: %w", err)
	}
	if err := s.Sys.Run(ctx, "curl", "-fsSL", "-o", s.Dst, s.URL); err != nil {
		return fmt.Errorf("download whisper model: %w", err)
	}
	return nil
}

// SoftwareConfig parameterises the uniform software set installed on every Mac.
type SoftwareConfig struct {
	BrewUser   string   // non-root brew user, e.g. "uknomi"
	BrewPath   string   // resolved brew binary, e.g. /opt/homebrew/bin/brew
	Formulae   []string // ffmpeg, tailscale, nmap, whisper-cpp
	EdgeUISrc  string   // packaged edge-ui binary
	EdgeUIDst  string   // /usr/local/bin/uknomi-edge-ui
	WhisperURL string
	WhisperDst string
}

// SoftwareSteps returns the uniform software steps in order: Homebrew, the
// formula set, the edge-ui binary, the whisper model (ADR-036 §2 — same on
// every device; the CP activates capabilities later). Colima + docker CLI are
// installed by the Colima slice (#89), not here.
func SoftwareSteps(sys System, c SoftwareConfig) []Step {
	return []Step{
		&HomebrewStep{Sys: sys, User: c.BrewUser},
		&BrewFormulaeStep{Sys: sys, User: c.BrewUser, BrewPath: c.BrewPath, Formulae: c.Formulae},
		&EnsureFileStep{Sys: sys, StepName: "edge-ui-binary", Src: c.EdgeUISrc, Dst: c.EdgeUIDst, Mode: 0o755},
		&WhisperModelStep{Sys: sys, URL: c.WhisperURL, Dst: c.WhisperDst},
	}
}
