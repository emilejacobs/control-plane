package ingest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
)

func TestPresenceSweeperSweepOncePersistsTransitions(t *testing.T) {
	t0 := time.Date(2026, 5, 21, 16, 0, 0, 0, time.UTC)
	p := presence.New()
	p.RecordHeartbeat("stale", t0)
	p.RecordHeartbeat("fresh", t0.Add(2*time.Minute))

	w := &fakePresenceWriter{}
	var logbuf bytes.Buffer
	sw := NewPresenceSweeper(p, w, SweeperConfig{
		Logger: slog.New(slog.NewJSONHandler(&logbuf, nil)),
		Now:    fixedClock(t0.Add(3 * time.Minute)), // stale 3m old, fresh 1m old
	})

	sw.sweepOnce(context.Background())
	if len(w.calls) != 1 || w.calls[0].deviceID != "stale" || w.calls[0].online {
		t.Fatalf("first sweep: got %+v want one {stale offline}", w.calls)
	}
	if !strings.Contains(logbuf.String(), "audit.presence") {
		t.Errorf("no audit.presence log line:\n%s", logbuf.String())
	}

	// A second sweep finds nothing new — Sweep is idempotent.
	sw.sweepOnce(context.Background())
	if len(w.calls) != 1 {
		t.Errorf("second sweep re-persisted an already-offline device: got %d calls want 1", len(w.calls))
	}
}

// TestPresenceSweeperEmitsTickHeartbeat is the Issue 21 sweeper-lag
// signal: every sweepOnce ends with a "sweeper.tick" log line, even when
// there are no transitions to persist. CloudWatch turns the line count
// into a metric and pages when it falls to zero — catching a stuck
// sweeper goroutine the lifecycle fast-path cannot otherwise reveal.
func TestPresenceSweeperEmitsTickHeartbeat(t *testing.T) {
	t0 := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	var logbuf bytes.Buffer
	sw := NewPresenceSweeper(presence.New(), &fakePresenceWriter{}, SweeperConfig{
		Logger: slog.New(slog.NewJSONHandler(&logbuf, nil)),
		Now:    fixedClock(t0),
	})

	sw.sweepOnce(context.Background()) // no transitions; still ticks
	if n := strings.Count(logbuf.String(), `"msg":"sweeper.tick"`); n != 1 {
		t.Errorf("sweeper.tick lines after one sweep: got %d want 1\nbuf:\n%s", n, logbuf.String())
	}

	sw.sweepOnce(context.Background())
	if n := strings.Count(logbuf.String(), `"msg":"sweeper.tick"`); n != 2 {
		t.Errorf("sweeper.tick lines after two sweeps: got %d want 2\nbuf:\n%s", n, logbuf.String())
	}
}

func TestPresenceSweeperRunStopsOnCancel(t *testing.T) {
	sw := NewPresenceSweeper(presence.New(), &fakePresenceWriter{}, SweeperConfig{
		Interval: 10 * time.Millisecond,
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sw.Run(ctx); close(done) }()

	time.Sleep(50 * time.Millisecond) // let several ticks fire
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper Run did not stop within 2s of ctx cancel")
	}
}

// TestPresenceSweeperRunsDBBackstop locks the stuck-online fix: every sweep
// also runs the DB-backed reconcile with cutoff = now - StaleThreshold, so
// orphaned devices the in-memory model never saw still get flipped offline.
func TestPresenceSweeperRunsDBBackstop(t *testing.T) {
	t0 := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	w := &fakePresenceWriter{reconcileN: 2}
	sw := NewPresenceSweeper(presence.New(), w, SweeperConfig{
		StaleThreshold: 5 * time.Minute,
		Logger:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Now:            fixedClock(t0),
	})

	sw.sweepOnce(context.Background())

	if len(w.reconcileCalls) != 1 {
		t.Fatalf("ReconcileStalePresence calls: got %d want 1", len(w.reconcileCalls))
	}
	c := w.reconcileCalls[0]
	if !c.now.Equal(t0) {
		t.Errorf("reconcile now: got %v want %v", c.now, t0)
	}
	if want := t0.Add(-5 * time.Minute); !c.staleBefore.Equal(want) {
		t.Errorf("reconcile staleBefore: got %v want %v", c.staleBefore, want)
	}
}
