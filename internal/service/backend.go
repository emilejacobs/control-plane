package service

import (
	"context"
	"errors"
)

type State string

const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateUnknown State = "unknown"
)

var ErrNotFound = errors.New("service not found")

type Backend interface {
	Status(ctx context.Context, name string) (State, error)
	Restart(ctx context.Context, name string) error
}

// ExecError reports a non-zero exit from the underlying OS tool (launchctl/systemctl)
// so the caller can surface stderr and the exit code on failure envelopes.
type ExecError struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExecError) Error() string {
	if e.Stderr != "" {
		return e.Stderr
	}
	return "exec failed"
}
