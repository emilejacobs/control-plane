package ingest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
)

type fakeSnapshotSweeperWriter struct {
	mu        sync.Mutex
	calls     []time.Time
	returnN   int
	returnErr error
}

func (w *fakeSnapshotSweeperWriter) DeleteSnapshotsOlderThan(_ context.Context, cutoff time.Time) (int, error) {
	w.mu.Lock()
	w.calls = append(w.calls, cutoff)
	w.mu.Unlock()
	return w.returnN, w.returnErr
}

// SweepOnce deletes rows older than now - threshold (default 90 days).
func TestSnapshotSweeperComputesCutoff(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	w := &fakeSnapshotSweeperWriter{returnN: 4}
	s := ingest.NewSnapshotSweeper(w, ingest.SnapshotSweeperConfig{
		Now: func() time.Time { return now },
	})

	s.SweepOnce(context.Background())

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(w.calls))
	}
	want := now.Add(-90 * 24 * time.Hour)
	if !w.calls[0].Equal(want) {
		t.Errorf("cutoff = %v, want %v (now - 90d)", w.calls[0], want)
	}
}

// A delete error doesn't crash the sweep (next tick retries).
func TestSnapshotSweeperToleratesDeleteError(t *testing.T) {
	w := &fakeSnapshotSweeperWriter{returnErr: errors.New("db down")}
	s := ingest.NewSnapshotSweeper(w, ingest.SnapshotSweeperConfig{})
	s.SweepOnce(context.Background()) // must not panic
	if len(w.calls) != 1 {
		t.Errorf("expected one attempt, got %d", len(w.calls))
	}
}
