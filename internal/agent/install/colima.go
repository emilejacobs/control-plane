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

// ColimaVMSize sizes the Colima VM to the host (mirrors migrate-colima.sh).
// ALPR inference is CPU-bound and Docker Desktop gave the container ~all the
// host's cores, so a fixed 2 vCPUs tanks recognition health. Leave 2 cores for
// macOS/agent/edge-ui; use ~half the RAM capped 4–8 GiB (ALPR's footprint is
// small); disk stays 30 GiB (screenshots/clips ride the host bind mount).
func ColimaVMSize(numCPU, memGiB int) (cpu, mem, disk int) {
	cpu = 2
	if numCPU > 4 {
		cpu = numCPU - 2
	}
	mem = memGiB / 2
	if mem < 4 {
		mem = 4
	}
	if mem > 8 {
		mem = 8
	}
	return cpu, mem, 30
}

// ColimaLaunchAgentPlist renders the user LaunchAgent that brings the per-user
// Colima VM up at login (ADR-038 option 3: the LaunchAgent owns the VM
// lifecycle, independent of the root agent). `colima start` is blocking and
// returns once the VM is up, so RunAtLoad (no KeepAlive) starts it once.
// --network-address + --network-preferred-route make the VZNAT reachable network
// the VM's default route, so the container reaches the directly-connected LAN
// camera (ADR-038; default NAT / plain --network-address cannot).
func ColimaLaunchAgentPlist(c ColimaAgentConfig) []byte {
	// launchd runs the LaunchAgent with a minimal PATH (/usr/bin:/bin:...). colima
	// shells out to limactl by bare name, so without the brew bin on PATH
	// `colima start` fatals ("limactl: executable file not found in $PATH") and
	// the VM never auto-starts at login. limactl is installed alongside colima, so
	// lead PATH with the colima binary's own dir (handles Apple Silicon
	// /opt/homebrew/bin and Intel /usr/local/bin).
	brewBin := filepath.Dir(c.ColimaPath)
	launchPath := brewBin + ":/usr/bin:/bin:/usr/sbin:/sbin"

	const tmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>%s</string>
    </dict>
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
        <string>--network-address</string>
        <string>--network-preferred-route</string>
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
		launchPath, c.Label, c.ColimaPath,
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
