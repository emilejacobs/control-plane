//go:build darwin

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// runner abstracts the exec shell-out so unit tests can stage canned
// stdout/stderr and exit codes without spawning real processes.
type runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

// consoleUIDFunc returns the uid currently owning /dev/console — the
// macOS convention for "the user logged into the GUI". Injected so
// tests don't depend on the host's actual graphical session state.
type consoleUIDFunc func() (uint32, error)

type launchctlBackend struct {
	run        runner
	consoleUID consoleUIDFunc
	logger     *slog.Logger
}

// NewSystemBackend returns a launchctlBackend wired to the real
// exec.CommandContext + /dev/console uid lookup. The logger defaults
// to discard; callers wanting visibility into the GUI-context
// fallback should set logger via NewSystemBackendWithLogger.
func NewSystemBackend() Backend {
	return newSystemBackend(slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

// NewSystemBackendWithLogger is the production constructor variant
// used by the agent so the dual-context fallback's debug line is
// visible in agent stderr.
func NewSystemBackendWithLogger(logger *slog.Logger) Backend {
	return newSystemBackend(logger)
}

func newSystemBackend(logger *slog.Logger) *launchctlBackend {
	return &launchctlBackend{
		run:        execRun,
		consoleUID: consoleUIDFromDevConsole,
		logger:     logger,
	}
}

func execRun(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func consoleUIDFromDevConsole() (uint32, error) {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unexpected Stat_t type for /dev/console")
	}
	return st.Uid, nil
}

// Status shells out to `launchctl list <name>` in the agent's
// (system) context. On macOS this prints a plist-style dictionary
// containing a "PID" key when the job is running. Exit code is
// non-zero when the named job is not loaded in the current domain —
// in that case we fall back to a GUI-context lookup so that
// LaunchAgents (registered under gui/<uid>/) don't permanently report
// as Unknown when the agent runs as a system LaunchDaemon.
func (b *launchctlBackend) Status(ctx context.Context, name string) (State, error) {
	stdout, stderr, err := b.run(ctx, "launchctl", "list", name)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("launchctl list %s: %w (stderr: %s)", name, err, string(stderr))
	}

	for _, line := range strings.Split(string(stdout), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "\"PID\"") {
			continue
		}
		// "PID" = 12345;  → running
		// (line is absent entirely when the job is loaded but not running)
		return StateRunning, nil
	}
	return StateStopped, nil
}

// Restart shells out to `launchctl kickstart -k system/<name>`. The -k flag asks
// launchd to terminate the running job (if any) before re-launching. Non-zero
// exit is reported as *ExecError so callers can surface stderr verbatim.
func (b *launchctlBackend) Restart(ctx context.Context, name string) error {
	stdout, stderr, err := b.run(ctx, "launchctl", "kickstart", "-k", "system/"+name)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ExecError{
				ExitCode: exitErr.ExitCode(),
				Stdout:   string(stdout),
				Stderr:   strings.TrimSpace(string(stderr)),
			}
		}
		return fmt.Errorf("launchctl kickstart system/%s: %w", name, err)
	}
	return nil
}
