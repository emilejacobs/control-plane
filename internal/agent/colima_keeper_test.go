package agent

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type fakeColimaEnsurer struct {
	mu sync.Mutex
	n  int
}

func (f *fakeColimaEnsurer) EnsureColima(context.Context) {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
}

func (f *fakeColimaEnsurer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// The keeper ensures Colima is up immediately at startup (the boot-race fix —
// don't wait a whole interval to discover Colima never came up), then ticks.
func TestRunColimaKeeper_EnsuresAtStartupThenStops(t *testing.T) {
	a := &Agent{logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	ce := &fakeColimaEnsurer{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: startup ensure runs, then the loop exits at once

	a.runColimaKeeper(ctx, ce, time.Hour)

	if ce.count() < 1 {
		t.Errorf("expected at least one EnsureColima at startup, got %d", ce.count())
	}
}
