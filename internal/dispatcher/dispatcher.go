package dispatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/emilejacobs/control-plane/internal/envelope"
)

type Handler interface {
	Handle(ctx context.Context, args json.RawMessage) (any, error)
}

type HandlerFunc func(ctx context.Context, args json.RawMessage) (any, error)

func (f HandlerFunc) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return f(ctx, args)
}

type Dispatcher struct {
	handlers map[string]Handler
	logger   *slog.Logger

	// verify, when set, authenticates a command before its handler runs;
	// requireSig is the set of command types it gates. Types outside the
	// set dispatch unsigned (ADR-028 forward-compat). See
	// WithSignatureVerification.
	verify     func(envelope.Command) error
	requireSig map[string]bool

	mu            sync.RWMutex
	lastCommandAt time.Time
}

// LastCommandAt returns the time of the most recent successfully dispatched
// command (handler returned without error), or the zero value if no command
// has been dispatched yet.
func (d *Dispatcher) LastCommandAt() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastCommandAt
}

type Option func(*Dispatcher)

func WithLogger(l *slog.Logger) Option {
	return func(d *Dispatcher) { d.logger = l }
}

// WithSignatureVerification gates the named command types on a valid envelope
// signature (issue #41, ADR-035 §2): before such a command's handler runs,
// verify must accept it, else the dispatch returns a command.bad_signature
// result and the handler never executes. Command types not listed dispatch
// unsigned — the forward-compat path for the Phase 0/2 unsigned handlers
// (ADR-028). The agent wires verify to cmdsign.VerifyCommand and requires the
// high-blast-radius "agent.update".
func WithSignatureVerification(verify func(envelope.Command) error, requiredTypes ...string) Option {
	return func(d *Dispatcher) {
		d.verify = verify
		d.requireSig = make(map[string]bool, len(requiredTypes))
		for _, t := range requiredTypes {
			d.requireSig[t] = true
		}
	}
}

func New(opts ...Option) *Dispatcher {
	d := &Dispatcher{
		handlers: make(map[string]Handler),
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func (d *Dispatcher) Register(commandType string, h Handler) {
	d.handlers[commandType] = h
}

func (d *Dispatcher) Dispatch(ctx context.Context, raw []byte) (out []byte, err error) {
	var cmd envelope.Command
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return nil, err
	}

	log := d.logger.With("correlation_id", cmd.CorrelationID, "command_type", cmd.Type)
	log.Info("dispatching command")

	h, ok := d.handlers[cmd.Type]
	if !ok {
		log.Warn("unknown command type")
		return json.Marshal(envelope.Result{
			CorrelationID: cmd.CorrelationID,
			CommandID:     cmd.CommandID,
			Type:          cmd.Type,
			Success:       false,
			Error: &envelope.ResultError{
				Code:    "command.unknown_type",
				Message: "unknown command type: " + cmd.Type,
			},
		})
	}

	// Authenticate a signature-required command before it runs. A forged or
	// unsigned agent.update is refused here — the handler never sees it.
	if d.verify != nil && d.requireSig[cmd.Type] {
		if err := d.verify(cmd); err != nil {
			log.Warn("command signature verification failed", "error", err)
			return json.Marshal(envelope.Result{
				CorrelationID: cmd.CorrelationID,
				CommandID:     cmd.CommandID,
				Type:          cmd.Type,
				Success:       false,
				Error: &envelope.ResultError{
					Code:    "command.bad_signature",
					Message: "command signature verification failed",
				},
			})
		}
	}

	defer func() {
		if r := recover(); r != nil {
			log.Error("handler panicked", "panic", r)
			out, err = json.Marshal(envelope.Result{
				CorrelationID: cmd.CorrelationID,
				CommandID:     cmd.CommandID,
				Type:          cmd.Type,
				Success:       false,
				Error: &envelope.ResultError{
					Code:    "handler.panic",
					Message: fmt.Sprintf("handler panic: %v", r),
				},
			})
		}
	}()

	result, herr := h.Handle(ctx, cmd.Args)
	if herr != nil {
		log.Warn("handler returned error", "error", herr)
		code, message := "handler.error", herr.Error()
		var coded *envelope.CodedError
		if errors.As(herr, &coded) {
			code, message = coded.Code, coded.Message
		}
		return json.Marshal(envelope.Result{
			CorrelationID: cmd.CorrelationID,
			CommandID:     cmd.CommandID,
			Type:          cmd.Type,
			Success:       false,
			Error: &envelope.ResultError{
				Code:    code,
				Message: message,
			},
		})
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.lastCommandAt = time.Now().UTC()
	d.mu.Unlock()

	log.Info("command handled")

	return json.Marshal(envelope.Result{
		CorrelationID: cmd.CorrelationID,
		CommandID:     cmd.CommandID,
		Type:          cmd.Type,
		Success:       true,
		Result:        resultBytes,
	})
}
