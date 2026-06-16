package install

import (
	"context"
	"fmt"
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
