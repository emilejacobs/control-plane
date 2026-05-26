//go:build darwin

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"testing"
)

// fakeRunner records every invocation and replays canned results
// keyed on the full argv (joined by spaces) so test cases can stage
// distinct outputs for system-context vs GUI-context calls.
type fakeRunner struct {
	results map[string]runResult
	calls   []string
}

type runResult struct {
	stdout   string
	stderr   string
	exitCode int // 0 = success; non-zero produces a synthetic *exec.ExitError
}

func (r *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	argv := name
	for _, a := range args {
		argv += " " + a
	}
	r.calls = append(r.calls, argv)
	res, ok := r.results[argv]
	if !ok {
		return nil, nil, &exec.ExitError{} // default to "missing" so unstubbed paths fail loudly
	}
	if res.exitCode != 0 {
		return []byte(res.stdout), []byte(res.stderr), &exec.ExitError{}
	}
	return []byte(res.stdout), []byte(res.stderr), nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// systemContextRunning verifies the slice-1 happy path is preserved
// through the injected runner: when `launchctl list <name>` exits 0
// and includes a "PID" line, Status returns StateRunning without
// touching the GUI fallback.
func TestStatus_SystemContext_Running(t *testing.T) {
	r := &fakeRunner{results: map[string]runResult{
		"launchctl list com.uknomi.agent": {
			stdout: "{\n\t\"Label\" = \"com.uknomi.agent\";\n\t\"PID\" = 12345;\n};\n",
		},
	}}
	b := &launchctlBackend{
		run:        r.run,
		consoleUID: func() (uint32, error) { t.Fatal("consoleUID should not be called"); return 0, nil },
		logger:     discardLogger(),
	}

	state, err := b.Status(context.Background(), "com.uknomi.agent")
	if err != nil {
		t.Fatalf("Status returned unexpected error: %v", err)
	}
	if state != StateRunning {
		t.Fatalf("Status = %q, want %q", state, StateRunning)
	}
	if len(r.calls) != 1 || r.calls[0] != "launchctl list com.uknomi.agent" {
		t.Fatalf("expected exactly one launchctl list call, got %v", r.calls)
	}
}

// systemContextStopped: launchctl list exits 0 but the PID line is
// absent (job loaded but not running) — should be StateStopped, no
// fallback.
func TestStatus_SystemContext_Stopped(t *testing.T) {
	r := &fakeRunner{results: map[string]runResult{
		"launchctl list com.uknomi.agent": {
			stdout: "{\n\t\"Label\" = \"com.uknomi.agent\";\n};\n",
		},
	}}
	b := &launchctlBackend{
		run:        r.run,
		consoleUID: func() (uint32, error) { t.Fatal("consoleUID should not be called"); return 0, nil },
		logger:     discardLogger(),
	}

	state, err := b.Status(context.Background(), "com.uknomi.agent")
	if err != nil {
		t.Fatalf("Status returned unexpected error: %v", err)
	}
	if state != StateStopped {
		t.Fatalf("Status = %q, want %q", state, StateStopped)
	}
}

// systemContextErrorNotExitError: a non-exit error (e.g. context
// cancellation, exec lookup failure) must surface as a wrapped error,
// not ErrNotFound — this is the transient-failure signal that callers
// use to log warn.
func TestStatus_SystemContext_TransientError(t *testing.T) {
	transient := errors.New("exec: lookup launchctl: not found")
	r := &fakeRunner{}
	b := &launchctlBackend{
		run: func(context.Context, string, ...string) ([]byte, []byte, error) {
			r.calls = append(r.calls, "called")
			return nil, nil, transient
		},
		consoleUID: func() (uint32, error) { t.Fatal("consoleUID should not be called"); return 0, nil },
		logger:     discardLogger(),
	}

	_, err := b.Status(context.Background(), "com.uknomi.agent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("transient failure must not collapse to ErrNotFound: %v", err)
	}
}

// silence unused-import nag when this file is the only one with bytes
var _ = bytes.NewBuffer
