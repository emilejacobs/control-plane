package install_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/install"
)

// fakeStep is a programmable Step for exercising the Runner. It records every
// Apply into a shared order slice so tests can assert sequencing, and can be
// told to fail Apply once and to flip IsDone→true after a successful Apply (so
// re-runs skip it — the partial-run-resume behaviour).
type fakeStep struct {
	name           string
	done           bool
	isDoneErr      error
	failApplyTimes int // Apply returns an error this many times, then succeeds
	doneAfterApply bool
	applies        int
	order          *[]string
}

func (s *fakeStep) Name() string { return s.name }

func (s *fakeStep) IsDone(context.Context) (bool, error) {
	if s.isDoneErr != nil {
		return false, s.isDoneErr
	}
	return s.done, nil
}

func (s *fakeStep) Apply(context.Context) error {
	s.applies++
	*s.order = append(*s.order, s.name)
	if s.failApplyTimes > 0 {
		s.failApplyTimes--
		return errors.New("apply boom")
	}
	if s.doneAfterApply {
		s.done = true
	}
	return nil
}

// Pending steps run in declaration order; each is applied exactly once.
func TestRunnerAppliesPendingInOrder(t *testing.T) {
	var order []string
	a := &fakeStep{name: "a", order: &order}
	b := &fakeStep{name: "b", order: &order}
	c := &fakeStep{name: "c", order: &order}

	if err := install.NewRunner(a, b, c).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := order; len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("execution order: got %v want [a b c]", got)
	}
}

// A step reporting done is skipped (Apply never called).
func TestRunnerSkipsDoneSteps(t *testing.T) {
	var order []string
	a := &fakeStep{name: "a", done: true, order: &order}
	b := &fakeStep{name: "b", order: &order}

	if err := install.NewRunner(a, b).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.applies != 0 {
		t.Errorf("done step a was applied %d times, want 0", a.applies)
	}
	if len(order) != 1 || order[0] != "b" {
		t.Errorf("order: got %v want [b]", order)
	}
}

// An IsDone error aborts the run and names the offending step.
func TestRunnerIsDoneErrorAborts(t *testing.T) {
	var order []string
	a := &fakeStep{name: "probe-fails", isDoneErr: errors.New("stat boom"), order: &order}
	b := &fakeStep{name: "b", order: &order}

	err := install.NewRunner(a, b).Run(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !contains(err.Error(), "probe-fails") {
		t.Errorf("error should name the step: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("nothing should have applied, got %v", order)
	}
}

// Apply error aborts at the failing step (later steps don't run), and on a
// re-run the already-completed earlier step is skipped while the failed step is
// retried — the partial-run-resume contract.
func TestRunnerPartialRunResumes(t *testing.T) {
	var order []string
	a := &fakeStep{name: "a", doneAfterApply: true, order: &order}
	b := &fakeStep{name: "b", failApplyTimes: 1, doneAfterApply: true, order: &order}
	c := &fakeStep{name: "c", order: &order}
	r := install.NewRunner(a, b, c)

	// First run: a applies, b fails, c never reached.
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("first run should fail at b")
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("first-run order: got %v want [a b]", order)
	}

	// Second run: a is done (skipped), b retried (now succeeds), c runs.
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if a.applies != 1 {
		t.Errorf("a applied %d times across both runs, want 1 (skipped on resume)", a.applies)
	}
	if b.applies != 2 {
		t.Errorf("b applied %d times, want 2 (retried on resume)", b.applies)
	}
	if c.applies != 1 {
		t.Errorf("c applied %d times, want 1", c.applies)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
