package install

import (
	"context"
	"fmt"
	"path/filepath"
)

// InstallBinariesStep lays down the agent binary (into the supervisor's
// agent-update/current slot) and the resident-wrapper supervisor. Idempotent
// by inspection: done once both destinations exist. Updates after enrollment
// are the self-update channel's job (ADR-035), so "exists" is the right check
// — a re-run of install never clobbers a self-updated binary.
type InstallBinariesStep struct {
	Sys           System
	AgentSrc      string // packaged binary, e.g. /pkg/uknomi-agent
	AgentDst      string // e.g. /var/uknomi/agent-update/current
	SupervisorSrc string
	SupervisorDst string // e.g. /usr/local/bin/uknomi-agent-supervisor
}

func (s *InstallBinariesStep) Name() string { return "install-binaries" }

func (s *InstallBinariesStep) IsDone(_ context.Context) (bool, error) {
	agent, err := s.Sys.Exists(s.AgentDst)
	if err != nil {
		return false, err
	}
	sup, err := s.Sys.Exists(s.SupervisorDst)
	if err != nil {
		return false, err
	}
	return agent && sup, nil
}

func (s *InstallBinariesStep) Apply(_ context.Context) error {
	if err := s.Sys.MkdirAll(filepath.Dir(s.AgentDst), 0o755); err != nil {
		return fmt.Errorf("mkdir agent dir: %w", err)
	}
	if err := s.Sys.CopyFile(s.AgentSrc, s.AgentDst, 0o755); err != nil {
		return fmt.Errorf("copy agent: %w", err)
	}
	if err := s.Sys.MkdirAll(filepath.Dir(s.SupervisorDst), 0o755); err != nil {
		return fmt.Errorf("mkdir supervisor dir: %w", err)
	}
	if err := s.Sys.CopyFile(s.SupervisorSrc, s.SupervisorDst, 0o755); err != nil {
		return fmt.Errorf("copy supervisor: %w", err)
	}
	return nil
}

// LaunchDaemonStep writes the agent LaunchDaemon plist and loads it. Idempotent
// by inspection: done when the plist exists AND launchd reports the label
// loaded — so if a prior run wrote the plist but the load failed, a re-run
// still loads it.
type LaunchDaemonStep struct {
	Sys       System
	Label     string
	PlistPath string
	Plist     []byte
}

func (s *LaunchDaemonStep) Name() string { return "launchdaemon" }

func (s *LaunchDaemonStep) IsDone(ctx context.Context) (bool, error) {
	exists, err := s.Sys.Exists(s.PlistPath)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	// `launchctl list <label>` exits non-zero when the job isn't loaded.
	if err := s.Sys.Run(ctx, "launchctl", "list", s.Label); err != nil {
		return false, nil
	}
	return true, nil
}

func (s *LaunchDaemonStep) Apply(ctx context.Context) error {
	if err := s.Sys.WriteFile(s.PlistPath, s.Plist, 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	// Best-effort unload so a stale definition is replaced cleanly.
	_ = s.Sys.Run(ctx, "launchctl", "unload", s.PlistPath)
	if err := s.Sys.Run(ctx, "launchctl", "load", "-w", s.PlistPath); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	return nil
}

// AgentDaemonConfig parameterises the agent LaunchDaemon plist.
type AgentDaemonConfig struct {
	Label          string
	SupervisorPath string // ProgramArguments[0] — launchd supervises the wrapper
	AgentDir       string // AGENT_DIR — the supervisor's agent-update dir
	ConfigPath     string // becomes AGENT_ARGS="--config <path>"
	StdoutPath     string
	StderrPath     string
}

// AgentLaunchDaemonPlist renders the LaunchDaemon plist. launchd supervises the
// resident wrapper (not the agent directly); the wrapper reads AGENT_DIR +
// AGENT_ARGS to gate a staged update then exec the current binary (ADR-035).
// KeepAlive lets launchd restart the wrapper, which the #65 watchdog relies on.
func AgentLaunchDaemonPlist(c AgentDaemonConfig) []byte {
	const tmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>AGENT_DIR</key>
        <string>%s</string>
        <key>AGENT_ARGS</key>
        <string>--config %s</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`
	return []byte(fmt.Sprintf(tmpl,
		c.Label, c.SupervisorPath, c.AgentDir, c.ConfigPath, c.StdoutPath, c.StderrPath))
}
