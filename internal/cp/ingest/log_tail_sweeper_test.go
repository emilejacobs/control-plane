package ingest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
)

type fakeLogTailSweeperWriter struct {
	mu        sync.Mutex
	calls     []time.Time
	returnN   int
	returnErr error
}

func (w *fakeLogTailSweeperWriter) DeleteStaleLogTails(_ context.Context, olderThan time.Time) (int, error) {
	w.mu.Lock()
	w.calls = append(w.calls, olderThan)
	w.mu.Unlock()
	return w.returnN, w.returnErr
}

// Sweeper passes a cutoff = now - threshold to the writer.
func TestLogTailSweeperComputesCutoff(t *testing.T) {
	now := time.Date(2026, 5, 24, 22, 0, 0, 0, time.UTC)
	w := &fakeLogTailSweeperWriter{returnN: 7}
	s := ingest.NewLogTailSweeper(w, ingest.LogTailSweeperConfig{
		Interval:  10 * time.Millisecond,
		Threshold: 24 * time.Hour,
		Now:       func() time.Time { return now },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	// Give one tick to fire, then stop.
	time.Sleep(25 * time.Millisecond)
	cancel()
	<-done

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) == 0 {
		t.Fatal("expected at least one DeleteStaleLogTails call")
	}
	want := now.Add(-24 * time.Hour)
	if !w.calls[0].Equal(want) {
		t.Errorf("cutoff: got %v, want %v", w.calls[0], want)
	}
}

// A writer error doesn't crash the sweeper — it logs and keeps ticking.
// (Returning would stop the goroutine and silently break the cleanup.)
func TestLogTailSweeperContinuesOnWriterError(t *testing.T) {
	w := &fakeLogTailSweeperWriter{returnErr: errors.New("transient db error")}
	s := ingest.NewLogTailSweeper(w, ingest.LogTailSweeperConfig{
		Interval:  5 * time.Millisecond,
		Threshold: 24 * time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) < 2 {
		t.Errorf("expected ≥2 calls despite errors; got %d", len(w.calls))
	}
}
