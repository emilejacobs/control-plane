package presence

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRecordHeartbeatUpdatesState(t *testing.T) {
	p := New()
	at := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	tr := p.RecordHeartbeat("dev-1", at)
	if !tr.Online || !tr.Changed {
		t.Errorf("first heartbeat: got %+v, want Online && Changed", tr)
	}
	if tr.DeviceID != "dev-1" {
		t.Errorf("transition device id: got %q want dev-1", tr.DeviceID)
	}

	got, ok := p.LastSeen("dev-1")
	if !ok {
		t.Fatal("dev-1 not known after heartbeat")
	}
	if !got.Equal(at) {
		t.Errorf("last seen: got %v want %v", got, at)
	}
}

func TestRecordHeartbeatSecondHeartbeatIsSteadyState(t *testing.T) {
	p := New()
	t0 := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	p.RecordHeartbeat("dev-1", t0)
	tr := p.RecordHeartbeat("dev-1", t0.Add(30*time.Second))
	if tr.Changed {
		t.Errorf("second heartbeat reported Changed; want steady state")
	}
	if !tr.Online {
		t.Errorf("second heartbeat: Online = false")
	}

	got, _ := p.LastSeen("dev-1")
	if !got.Equal(t0.Add(30 * time.Second)) {
		t.Errorf("last seen not advanced: got %v want %v", got, t0.Add(30*time.Second))
	}
}

func TestRecordHeartbeatIsolatesDevices(t *testing.T) {
	p := New()
	t0 := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	p.RecordHeartbeat("dev-a", t0)
	p.RecordHeartbeat("dev-b", t0.Add(time.Minute))

	a, _ := p.LastSeen("dev-a")
	if !a.Equal(t0) {
		t.Errorf("dev-a last seen: got %v want %v", a, t0)
	}
	b, _ := p.LastSeen("dev-b")
	if !b.Equal(t0.Add(time.Minute)) {
		t.Errorf("dev-b last seen: got %v want %v", b, t0.Add(time.Minute))
	}
	if _, ok := p.LastSeen("dev-unknown"); ok {
		t.Errorf("unknown device reported as known")
	}
}

// TestRecordHeartbeatConcurrentDifferentDevices exercises the mutex: run
// with -race to catch unsynchronized map access.
func TestRecordHeartbeatConcurrentDifferentDevices(t *testing.T) {
	p := New()
	at := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	const devices = 50
	var wg sync.WaitGroup
	for i := 0; i < devices; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.RecordHeartbeat(fmt.Sprintf("dev-%d", id), at)
		}(i)
	}
	wg.Wait()

	for i := 0; i < devices; i++ {
		if _, ok := p.LastSeen(fmt.Sprintf("dev-%d", i)); !ok {
			t.Errorf("dev-%d missing after concurrent heartbeats", i)
		}
	}
}

func TestIsOnline(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		lastSeen time.Time
		want     bool
	}{
		{"never seen", time.Time{}, false},
		{"just now", now, true},
		{"60s ago", now.Add(-60 * time.Second), true},
		{"exactly at threshold", now.Add(-OnlineThreshold), true},
		{"one second past threshold", now.Add(-OnlineThreshold - time.Second), false},
		{"ten minutes ago", now.Add(-10 * time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOnline(tc.lastSeen, now); got != tc.want {
				t.Errorf("IsOnline(%v, %v) = %v, want %v", tc.lastSeen, now, got, tc.want)
			}
		})
	}
}
