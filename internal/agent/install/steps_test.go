package install_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/install"
)

// fakeSystem is an in-memory System. It models file existence, captures copies
// and writes, and tracks launchd "loaded" state so the LaunchDaemon step's
// IsDone/Apply round-trip is exercisable without a real host.
type fakeSystem struct {
	exists       map[string]bool
	wrote        map[string][]byte
	copies       map[string]string // dst -> src
	runs         [][]string
	loaded       bool
	installed    map[string]bool // brew formulae present
	installCount int             // brew install invocations
	runErr       error
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{
		exists:    map[string]bool{},
		wrote:     map[string][]byte{},
		copies:    map[string]string{},
		installed: map[string]bool{},
	}
}

func (f *fakeSystem) Exists(path string) (bool, error) { return f.exists[path], nil }

func (f *fakeSystem) WriteFile(path string, data []byte, _ os.FileMode) error {
	f.wrote[path] = data
	f.exists[path] = true
	return nil
}

func (f *fakeSystem) MkdirAll(path string, _ os.FileMode) error {
	f.exists[path] = true
	return nil
}

func (f *fakeSystem) CopyFile(src, dst string, _ os.FileMode) error {
	f.copies[dst] = src
	f.exists[dst] = true
	return nil
}

func (f *fakeSystem) Run(_ context.Context, name string, args ...string) error {
	call := append([]string{name}, args...)
	f.runs = append(f.runs, call)
	if f.runErr != nil {
		return f.runErr
	}
	// Model launchctl load/unload/list against the loaded flag.
	if name == "launchctl" && len(args) >= 1 {
		switch args[0] {
		case "load":
			f.loaded = true
		case "unload":
			f.loaded = false
		case "list":
			if !f.loaded {
				return errors.New("not loaded")
			}
		}
	}
	// Model `curl -o <dst> <url>` as a download that makes dst exist.
	if name == "curl" {
		for i, a := range args {
			if a == "-o" && i+1 < len(args) {
				f.exists[args[i+1]] = true
			}
		}
		return nil
	}
	// Model `sudo -u <user> brew {list,install} <formula>` against the
	// installed set; an install.sh-bearing command (Homebrew bootstrap) and
	// anything else succeed.
	if name == "sudo" {
		for i, a := range args {
			switch a {
			case "install":
				if i+1 < len(args) {
					f.installed[args[i+1]] = true
					f.installCount++
					return nil
				}
			case "list":
				formula := args[len(args)-1]
				if !f.installed[formula] {
					return errors.New("not installed")
				}
				return nil
			}
		}
	}
	return nil
}

func (f *fakeSystem) ran(sub string) bool {
	for _, c := range f.runs {
		if strings.Contains(strings.Join(c, " "), sub) {
			return true
		}
	}
	return false
}

// InstallBinaries copies the agent + supervisor when absent, and is a no-op
// (IsDone true) once both are present.
func TestInstallBinariesStep(t *testing.T) {
	fs := newFakeSystem()
	step := &install.InstallBinariesStep{
		Sys:           fs,
		AgentSrc:      "/pkg/uknomi-agent",
		AgentDst:      "/var/uknomi/agent-update/current",
		SupervisorSrc: "/pkg/uknomi-agent-supervisor",
		SupervisorDst: "/usr/local/bin/uknomi-agent-supervisor",
	}

	done, err := step.IsDone(context.Background())
	if err != nil || done {
		t.Fatalf("IsDone before apply: got (%v,%v) want (false,nil)", done, err)
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if fs.copies["/var/uknomi/agent-update/current"] != "/pkg/uknomi-agent" {
		t.Errorf("agent not copied to current: %v", fs.copies)
	}
	if fs.copies["/usr/local/bin/uknomi-agent-supervisor"] != "/pkg/uknomi-agent-supervisor" {
		t.Errorf("supervisor not copied: %v", fs.copies)
	}
	done, err = step.IsDone(context.Background())
	if err != nil || !done {
		t.Errorf("IsDone after apply: got (%v,%v) want (true,nil)", done, err)
	}
}

// LaunchDaemon writes the plist and loads it; IsDone is false when the plist is
// absent or not loaded, and true once both hold.
func TestLaunchDaemonStep(t *testing.T) {
	fs := newFakeSystem()
	plist := install.AgentLaunchDaemonPlist(install.AgentDaemonConfig{
		Label:          "com.uknomi.agent",
		SupervisorPath: "/usr/local/bin/uknomi-agent-supervisor",
		AgentDir:       "/var/uknomi/agent-update",
		ConfigPath:     "/var/uknomi/agent-config.json",
		StdoutPath:     "/var/log/uknomi-agent.log",
		StderrPath:     "/var/log/uknomi-agent-error.log",
	})
	step := &install.LaunchDaemonStep{
		Sys:       fs,
		Label:     "com.uknomi.agent",
		PlistPath: "/Library/LaunchDaemons/com.uknomi.agent.plist",
		Plist:     plist,
	}

	done, _ := step.IsDone(context.Background())
	if done {
		t.Fatal("IsDone should be false before apply")
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := fs.wrote["/Library/LaunchDaemons/com.uknomi.agent.plist"]; !ok {
		t.Error("plist not written")
	}
	if !fs.ran("launchctl load") {
		t.Errorf("launchctl load not invoked; runs=%v", fs.runs)
	}
	done, err := step.IsDone(context.Background())
	if err != nil || !done {
		t.Errorf("IsDone after apply: got (%v,%v) want (true,nil)", done, err)
	}
}

// The generated plist supervises the wrapper (not the agent directly) and
// carries the AGENT_DIR/AGENT_ARGS contract the supervisor reads, with
// KeepAlive so launchd restarts it (ADR-035).
func TestAgentLaunchDaemonPlistContents(t *testing.T) {
	plist := string(install.AgentLaunchDaemonPlist(install.AgentDaemonConfig{
		Label:          "com.uknomi.agent",
		SupervisorPath: "/usr/local/bin/uknomi-agent-supervisor",
		AgentDir:       "/var/uknomi/agent-update",
		ConfigPath:     "/var/uknomi/agent-config.json",
		StdoutPath:     "/var/log/uknomi-agent.log",
		StderrPath:     "/var/log/uknomi-agent-error.log",
	}))
	for _, want := range []string{
		"com.uknomi.agent",
		"/usr/local/bin/uknomi-agent-supervisor",
		"AGENT_DIR",
		"/var/uknomi/agent-update",
		"AGENT_ARGS",
		"--config /var/uknomi/agent-config.json",
		"KeepAlive",
		"RunAtLoad",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q\n%s", want, plist)
		}
	}
}
