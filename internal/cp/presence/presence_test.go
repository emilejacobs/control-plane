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

var t0 = time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

// Behavior 1: a stale device is emitted by Sweep.
func TestSweepEmitsStaleDevice(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0)

	got := p.Sweep(t0.Add(OnlineThreshold + time.Second))
	if len(got) != 1 {
		t.Fatalf("Sweep transitions: got %d want 1", len(got))
	}
	if got[0].DeviceID != "dev-1" || got[0].Online || !got[0].Changed {
		t.Errorf("transition: got %+v want dev-1 offline+changed", got[0])
	}
}

// Behavior 2: a fresh device is not emitted.
func TestSweepIgnoresFreshDevice(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0)

	if got := p.Sweep(t0.Add(60 * time.Second)); len(got) != 0 {
		t.Errorf("Sweep emitted a fresh device: %+v", got)
	}
}

// Behavior 3: successive Sweeps do not re-emit an already-offline device.
func TestSweepIsIdempotent(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0)

	first := p.Sweep(t0.Add(OnlineThreshold + time.Second))
	if len(first) != 1 {
		t.Fatalf("first sweep: got %d transitions want 1", len(first))
	}
	if second := p.Sweep(t0.Add(OnlineThreshold + 2*time.Second)); len(second) != 0 {
		t.Errorf("second sweep re-emitted an already-offline device: %+v", second)
	}
}

// Sweep emits only the stale devices, leaving fresh ones alone.
func TestSweepEmitsOnlyStaleDevices(t *testing.T) {
	p := New()
	p.RecordHeartbeat("stale", t0)
	p.RecordHeartbeat("fresh", t0.Add(85*time.Second))

	got := p.Sweep(t0.Add(OnlineThreshold + 5*time.Second)) // stale 95s, fresh 10s
	if len(got) != 1 || got[0].DeviceID != "stale" {
		t.Errorf("Sweep: got %+v want only [stale]", got)
	}
}

// Behavior 4: a disconnect emits an offline transition immediately, without
// waiting for the sweeper.
func TestOnDisconnectEmitsImmediately(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0) // online, fresh

	tr := p.OnDisconnect("dev-1", t0.Add(time.Second))
	if tr.Online || !tr.Changed {
		t.Errorf("disconnect of an online device: got %+v want offline+changed", tr)
	}
	if got := p.Sweep(t0.Add(2 * time.Second)); len(got) != 0 {
		t.Errorf("Sweep re-emitted a device already offline via disconnect: %+v", got)
	}
}

// A disconnect for an already-offline device is a no-op.
func TestOnDisconnectOfOfflineDeviceIsNoOp(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0)
	p.OnDisconnect("dev-1", t0.Add(time.Second)) // now offline

	if tr := p.OnDisconnect("dev-1", t0.Add(2*time.Second)); tr.Changed {
		t.Errorf("second disconnect reported a change: %+v", tr)
	}
}

// Behavior 5: a reconnect emits an online transition.
func TestOnConnectEmitsOnline(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0)
	p.OnDisconnect("dev-1", t0.Add(time.Second)) // offline

	tr := p.OnConnect("dev-1", t0.Add(2*time.Second))
	if !tr.Online || !tr.Changed {
		t.Errorf("reconnect of an offline device: got %+v want online+changed", tr)
	}
}

// A device that reconnects after a long offline gap must not be immediately
// re-swept offline — OnConnect refreshes last_seen.
func TestOnConnectRefreshesLastSeen(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0)
	p.OnDisconnect("dev-1", t0.Add(time.Second))

	reconnectAt := t0.Add(10 * time.Minute)
	p.OnConnect("dev-1", reconnectAt)
	if got := p.Sweep(reconnectAt.Add(30 * time.Second)); len(got) != 0 {
		t.Errorf("Sweep offlined a device that reconnected 30s ago: %+v", got)
	}
}

// Behavior 6: a connect for an already-online device is a no-op.
func TestOnConnectOfOnlineDeviceIsNoOp(t *testing.T) {
	p := New()
	p.RecordHeartbeat("dev-1", t0) // online

	if tr := p.OnConnect("dev-1", t0.Add(time.Second)); tr.Changed {
		t.Errorf("connect of an already-online device reported a change: %+v", tr)
	}
}

// Behavior 7: the freshness threshold is configurable at construction.
func TestSweepThresholdConfigurableAtConstruction(t *testing.T) {
	p := New(WithThreshold(10 * time.Second))
	p.RecordHeartbeat("dev-1", t0)

	if got := p.Sweep(t0.Add(9 * time.Second)); len(got) != 0 {
		t.Errorf("Sweep offlined a device inside the 10s threshold: %+v", got)
	}
	if got := p.Sweep(t0.Add(11 * time.Second)); len(got) != 1 {
		t.Errorf("Sweep with 10s threshold at 11s: got %d transitions want 1", len(got))
	}
}
