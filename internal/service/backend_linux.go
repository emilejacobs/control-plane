//go:build linux

package service

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type systemctlBackend struct{}

func NewSystemBackend() Backend { return &systemctlBackend{} }

// Status shells out to `systemctl show --property=LoadState,ActiveState <name>`,
// which returns key=value lines regardless of unit existence and uses the
// "LoadState=not-found" marker to distinguish missing units from inactive ones.
func (b *systemctlBackend) Status(ctx context.Context, name string) (State, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "systemctl", "show", "--property=LoadState,ActiveState", name)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemctl show %s: %w (stderr: %s)", name, err, stderr.String())
	}

	var loadState, activeState string
	for _, line := range strings.Split(stdout.String(), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "LoadState":
			loadState = v
		case "ActiveState":
			activeState = v
		}
	}

	if loadState == "not-found" || loadState == "" {
		return "", ErrNotFound
	}
	if activeState == "active" || activeState == "activating" {
		return StateRunning, nil
	}
	return StateStopped, nil
}
