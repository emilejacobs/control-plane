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
}
