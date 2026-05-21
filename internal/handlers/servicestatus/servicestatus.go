package servicestatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/service"
)

type Request struct {
	Name string `json:"name"`
}

type Response struct {
	Name  string        `json:"name"`
	State service.State `json:"state"`
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

	state, err := h.backend.Status(ctx, req.Name)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return nil, envelope.NewCodedError("service.not_found", fmt.Sprintf("service %q not found", req.Name))
		}
		return nil, err
	}

	return Response{Name: req.Name, State: state}, nil
}
