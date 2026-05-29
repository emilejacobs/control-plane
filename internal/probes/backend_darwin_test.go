//go:build darwin

package probes

import (
	"context"
	"os"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// fakeRunner stages canned stdout/stderr/exit per command so probe
// methods can be exercised without shelling out to real macOS tools.
// Commands are keyed by name + space-joined args.
type fakeRunner struct {
	results map[string]cmdResult
}

type cmdResult struct {
	stdout string
	stderr string
	err    error
}

func (f fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	key := name
	for _, a := range args {
		key += " " + a
	}
	r, ok := f.results[key]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	return []byte(r.stdout), []byte(r.stderr), r.err
}

// fakeStat stages canned file metadata per path.
func fakeStat(files map[string]fileStat) statFunc {
	return func(path string) (fileStat, error) {
		fs, ok := files[path]
		if !ok {
			return fileStat{}, os.ErrNotExist
		}
		return fs, nil
	}
}

func TestProbeAutoLoginConfigured(t *testing.T) {
	b := &darwinBackend{
		run: fakeRunner{results: map[string]cmdResult{
			"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "uknomi\n"},
		}}.run,
		stat: fakeStat(map[string]fileStat{
			"/etc/kcpassword": {Mode: 0o600, UID: 0, GID: 0},
		}),
		expectedLoginUser: "uknomi",
	}

	res := b.probeAutoLogin(context.Background())

	if res.Name != healthprobes.ProbeAutoLogin {
		t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbeAutoLogin)
	}
	if res.State != "configured" {
		t.Errorf("State = %q, want %q", res.State, "configured")
	}
	if res.Status != healthprobes.StatusGreen {
		t.Errorf("Status = %q, want %q", res.Status, healthprobes.StatusGreen)
	}
}

func TestProbeAutoLoginMissing(t *testing.T) {
	cases := map[string]struct {
		runResults map[string]cmdResult
		files      map[string]fileStat
	}{
		"autoLoginUser key absent": {
			// `defaults read` exits non-zero when the key is unset.
			runResults: map[string]cmdResult{},
			files:      map[string]fileStat{"/etc/kcpassword": {Mode: 0o600}},
		},
		"autoLoginUser is a different user": {
			runResults: map[string]cmdResult{
				"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "admin\n"},
			},
			files: map[string]fileStat{"/etc/kcpassword": {Mode: 0o600}},
		},
		"kcpassword absent (the decay failure)": {
			runResults: map[string]cmdResult{
				"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "uknomi\n"},
			},
			files: map[string]fileStat{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := &darwinBackend{
				run:               fakeRunner{results: tc.runResults}.run,
				stat:              fakeStat(tc.files),
				expectedLoginUser: "uknomi",
			}
			res := b.probeAutoLogin(context.Background())
			if res.State != "missing" {
				t.Errorf("State = %q, want %q", res.State, "missing")
			}
			if res.Status != healthprobes.StatusRed {
				t.Errorf("Status = %q, want red", res.Status)
			}
		})
	}
}

func TestProbeGUISession(t *testing.T) {
	cases := map[string]struct {
		consoleUser string
		wantState   string
		wantStatus  healthprobes.Status
	}{
		"expected user active":       {"uknomi", "active", healthprobes.StatusGreen},
		"login window (root owns)":   {"root", "login_window", healthprobes.StatusRed},
		"different user lingering":   {"admin", "different_user", healthprobes.StatusYellow},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := &darwinBackend{
				run: fakeRunner{results: map[string]cmdResult{
					"stat -f %Su /dev/console": {stdout: tc.consoleUser + "\n"},
				}}.run,
				expectedLoginUser: "uknomi",
			}
			res := b.probeGUISession(context.Background())
			if res.Name != healthprobes.ProbeGUISession {
				t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbeGUISession)
			}
			if res.State != tc.wantState {
				t.Errorf("State = %q, want %q", res.State, tc.wantState)
			}
			if res.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", res.Status, tc.wantStatus)
			}
			if res.Details["console_user"] != tc.consoleUser {
				t.Errorf("Details[console_user] = %v, want %q", res.Details["console_user"], tc.consoleUser)
			}
		})
	}
}

func TestProbeAutoLoginCorrupted(t *testing.T) {
	cases := map[string]fileStat{
		"world-readable mode":  {Mode: 0o644, UID: 0, GID: 0},
		"not owned by root":    {Mode: 0o600, UID: 501, GID: 20},
	}
	for name, fs := range cases {
		t.Run(name, func(t *testing.T) {
			b := &darwinBackend{
				run: fakeRunner{results: map[string]cmdResult{
					"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "uknomi\n"},
				}}.run,
				stat:              fakeStat(map[string]fileStat{"/etc/kcpassword": fs}),
				expectedLoginUser: "uknomi",
			}
			res := b.probeAutoLogin(context.Background())
			if res.State != "corrupted" {
				t.Errorf("State = %q, want %q", res.State, "corrupted")
			}
			if res.Status != healthprobes.StatusRed {
				t.Errorf("Status = %q, want red", res.Status)
			}
		})
	}
}
