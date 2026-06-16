package install

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
)

// ColimaAgentConfig parameterises the Colima user LaunchAgent.
type ColimaAgentConfig struct {
	Label      string // com.uknomi.colima
	ColimaPath string // /opt/homebrew/bin/colima
	CPU        int
	MemoryGiB  int
	DiskGiB    int
	MountDir   string // bind-mounted into the VM (:w) so config.ini round-trips
	StdoutPath string
	StderrPath string
}

// ColimaLaunchAgentPlist renders the user LaunchAgent that brings the per-user
// Colima VM up at login (ADR-038 option 3: the LaunchAgent owns the VM
// lifecycle, independent of the root agent). `colima start` is blocking and
// returns once the VM is up, so RunAtLoad (no KeepAlive) starts it once.
func ColimaLaunchAgentPlist(c ColimaAgentConfig) []byte {
	const tmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>start</string>
        <string>--cpu</string>
        <string>%s</string>
        <string>--memory</string>
        <string>%s</string>
        <string>--disk</string>
        <string>%s</string>
        <string>--vm-type</string>
        <string>vz</string>
        <string>--mount</string>
        <string>%s:w</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`
	return []byte(fmt.Sprintf(tmpl,
		c.Label, c.ColimaPath,
		strconv.Itoa(c.CPU), strconv.Itoa(c.MemoryGiB), strconv.Itoa(c.DiskGiB),
		c.MountDir, c.StdoutPath, c.StderrPath))
}

// ColimaLaunchAgentStep writes the Colima user LaunchAgent. Idempotent by
// inspection (the plist exists). Loading happens at the next login — the agent
// (root) can't load a user LaunchAgent into a session it isn't in.
type ColimaLaunchAgentStep struct {
	Sys   System
	Path  string
	Plist []byte
}

func (s *ColimaLaunchAgentStep) Name() string { return "colima-launchagent" }

func (s *ColimaLaunchAgentStep) IsDone(_ context.Context) (bool, error) {
	return s.Sys.Exists(s.Path)
}

func (s *ColimaLaunchAgentStep) Apply(_ context.Context) error {
	if err := s.Sys.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents dir: %w", err)
	}
	if err := s.Sys.WriteFile(s.Path, s.Plist, 0o644); err != nil {
		return fmt.Errorf("write colima LaunchAgent: %w", err)
	}
	return nil
}

// ColimaConfig parameterises the Colima install steps.
type ColimaConfig struct {
	BrewUser         string
	BrewPath         string
	LaunchAgentPath  string
	LaunchAgentPlist []byte
}

// ColimaSteps installs colima + the docker CLI (as the brew user) and writes
// the Colima user LaunchAgent. It deliberately does NOT start the ALPR
// container — that awaits Commission, when the CP pushes the license (ADR-036).
func ColimaSteps(sys System, c ColimaConfig) []Step {
	return []Step{
		&BrewFormulaeStep{Sys: sys, User: c.BrewUser, BrewPath: c.BrewPath, Formulae: []string{"colima", "docker"}},
		&ColimaLaunchAgentStep{Sys: sys, Path: c.LaunchAgentPath, Plist: c.LaunchAgentPlist},
	}
}
