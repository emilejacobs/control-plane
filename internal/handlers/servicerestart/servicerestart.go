package servicerestart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/service"
)

type Request struct {
	Name string `json:"name"`
}

type Response struct {
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type Handler struct {
	backend service.Backend
}

func New(backend service.Backend) *Handler {
	return &Handler{backend: backend}
}

func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var req Request
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	}
	if req.Name == "" {
		return nil, fmt.Errorf("missing required argument: name")
	}

	startedAt := time.Now().UTC()
	if err := h.backend.Restart(ctx, req.Name); err != nil {
		var execErr *service.ExecError
		if errors.As(err, &execErr) {
			return nil, envelope.NewCodedError(
				"service.restart_failed",
				fmt.Sprintf("%s (exit %d)", execErr.Stderr, execErr.ExitCode),
			)
		}
		return nil, err
	}
	finishedAt := time.Now().UTC()

	return Response{
		Name:       req.Name,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}, nil
}
