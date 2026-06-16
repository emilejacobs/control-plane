// Package install is the device-side Provision orchestration (ADR-036/037):
// `uknomi-agent install` self-configures the host, installs the uniform
// software set, lays down the supervisor + LaunchDaemon, and enrolls.
//
// The orchestration is a list of idempotent-by-inspection Steps run in order.
// Each Step reports whether it IsDone by inspecting real system state (is the
// binary present? is the cert on disk? is the daemon loaded?) rather than
// consulting a separate state ledger — so a re-run resumes a partial install
// and never trusts a stale record (ADR-037 §5; replaces the old inline-python3
// JSON state).
package install

import (
	"context"
	"fmt"
)

// Step is one idempotent unit of the install. IsDone inspects live system
// state; Apply performs the mutation. A Step's Apply is only called when
// IsDone reported false.
type Step interface {
	Name() string
	IsDone(ctx context.Context) (bool, error)
	Apply(ctx context.Context) error
}

// Runner executes Steps in order, skipping those already done.
type Runner struct {
	steps []Step
	logf  func(format string, args ...any)
}

// NewRunner returns a Runner over the given steps, in order.
func NewRunner(steps ...Step) *Runner {
	return &Runner{steps: steps, logf: func(string, ...any) {}}
}

// WithLogf sets a logging callback (e.g. wrapping slog) reporting which steps
// are skipped vs applied. Returns the Runner for chaining.
func (r *Runner) WithLogf(fn func(format string, args ...any)) *Runner {
	if fn != nil {
		r.logf = fn
	}
	return r
}

// Run executes each step in order. A step that already IsDone is skipped; one
// that is pending is Applied. The first IsDone or Apply error aborts the run
// (later steps do not run) and is returned wrapped with the step name, so a
// re-run resumes from the failed step.
func (r *Runner) Run(ctx context.Context) error {
	for _, s := range r.steps {
		done, err := s.IsDone(ctx)
		if err != nil {
			return fmt.Errorf("install step %q: check: %w", s.Name(), err)
		}
		if done {
			r.logf("install step %q: already done, skipping", s.Name())
			continue
		}
		r.logf("install step %q: applying", s.Name())
		if err := s.Apply(ctx); err != nil {
			return fmt.Errorf("install step %q: %w", s.Name(), err)
		}
	}
	return nil
}
