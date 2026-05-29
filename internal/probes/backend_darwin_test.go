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
