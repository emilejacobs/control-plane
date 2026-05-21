//go:build darwin

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type launchctlBackend struct{}

func NewSystemBackend() Backend { return &launchctlBackend{} }

// Status shells out to `launchctl list <name>`. On macOS this prints a plist-style
// dictionary containing a "PID" key when the job is running. Exit code is non-zero
// when the named job is not loaded.
func (b *launchctlBackend) Status(ctx context.Context, name string) (State, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "launchctl", "list", name)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("launchctl list %s: %w (stderr: %s)", name, err, stderr.String())
	}

	for _, line := range strings.Split(stdout.String(), "\n") {
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
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", "system/"+name)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ExecError{
				ExitCode: exitErr.ExitCode(),
				Stdout:   stdout.String(),
				Stderr:   strings.TrimSpace(stderr.String()),
			}
		}
		return fmt.Errorf("launchctl kickstart system/%s: %w", name, err)
	}
	return nil
}
