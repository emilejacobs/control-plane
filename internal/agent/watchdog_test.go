package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The transport watchdog (#65) detects a wedged MQTT session — paho believes it
// is disconnected and never re-establishes, so publishes (heartbeats) stop
// succeeding for hours — and forces recovery. Liveness is "time since the last
// successful publish"; the heartbeat ticks every 30s, so a multi-minute stale
// window means the session is dead, not merely between beats.

func TestWatchdogFiresWhenPublishesGoStale(t *testing.T) {
	base := time.Now()
	fired := make(chan struct{})
	w := &watchdog{
		// Last success was 10 min ago — well past the 5-min window.
		lastSuccess:   func() time.Time { return base.Add(-10 * time.Minute) },
		now:           func() time.Time { return base },
		staleAfter:    5 * time.Minute,
		checkInterval: time.Millisecond,
		onWedged:      func() { close(fired) },
	}
	go w.run(context.Background())

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire on a stale session")
	}
}

func TestWatchdogDoesNotFireWhileHealthy(t *testing.T) {
	base := time.Now()
	var mu sync.Mutex
	count := 0
	w := &watchdog{
		// A healthy session: last success was 20s ago (one beat), within window.
		lastSuccess:   func() time.Time { return base.Add(-20 * time.Second) },
		now:           func() time.Time { return base },
		staleAfter:    5 * time.Minute,
		checkInterval: time.Millisecond,
		onWedged:      func() { mu.Lock(); count++; mu.Unlock() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.run(ctx); close(done) }()

	// Let many check intervals elapse, then stop.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Fatalf("watchdog fired %d times on a healthy session, want 0", count)
	}
}

func TestWatchdogStopsOnContextCancel(t *testing.T) {
	base := time.Now()
	fired := false
	w := &watchdog{
		lastSuccess:   func() time.Time { return base.Add(-10 * time.Minute) },
		now:           func() time.Time { return base },
		staleAfter:    5 * time.Minute,
		checkInterval: time.Hour, // never ticks within the test
		onWedged:      func() { fired = true },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	done := make(chan struct{})
	go func() { w.run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not return on context cancel")
	}
	if fired {
		t.Fatal("watchdog fired despite a cancelled context")
	}
}
