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
