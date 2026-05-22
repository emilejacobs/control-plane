// Package presence owns device freshness: the online threshold, the
// in-memory per-device presence state, and the transitions that drive it.
//
// Per PRD § Module decomposition, Presence is the single home for the
// online/offline model. RecordHeartbeat and the IoT "connected" lifecycle
// event move a device online; Sweep (heartbeat staleness) and the
// "disconnected" lifecycle event move it offline. The module is DB-free and
// clock-free — time is always a parameter — so it is exhaustively
// unit-testable; cp-ingest's sweeper goroutine and ingesters persist the
// transitions it returns.
package presence

import (
	"sync"
	"time"
)

// OnlineThreshold is the default freshness window: a device whose last
// heartbeat is within this much of "now" counts as online. 90s is 3× the
// 30s heartbeat interval, tolerating one missed publish without flapping
// (PRD User Story 9). Override per-instance with WithThreshold.
const OnlineThreshold = 90 * time.Second

// IsOnline reports whether a device last seen at lastSeen is online as of
// now, against the default OnlineThreshold. A zero lastSeen (never seen) is
// always offline.
func IsOnline(lastSeen, now time.Time) bool {
	if lastSeen.IsZero() {
		return false
	}
	return now.Sub(lastSeen) <= OnlineThreshold
}

// Transition reports a device's presence state after an event and whether
// that event changed it. RecordHeartbeat, OnConnect, and OnDisconnect each
// return one; Sweep returns one per device that changed on that call.
type Transition struct {
	DeviceID string
	Online   bool
	Changed  bool
}

type deviceState struct {
	lastSeen time.Time
	online   bool
}

// Presence holds in-memory presence state per device. Safe for concurrent
// use: events for different devices are independent.
type Presence struct {
	mu        sync.Mutex
	devices   map[string]*deviceState
	threshold time.Duration
}

// Option configures a Presence at construction.
type Option func(*Presence)

// WithThreshold overrides the default OnlineThreshold — used by tests and
// by callers that want a non-default freshness window.
func WithThreshold(d time.Duration) Option {
	return func(p *Presence) { p.threshold = d }
}

func New(opts ...Option) *Presence {
	p := &Presence{
		devices:   make(map[string]*deviceState),
		threshold: OnlineThreshold,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// RecordHeartbeat marks deviceID as seen at at and online. The returned
// Transition reports whether this heartbeat brought the device online —
// true for its first heartbeat, or the first after Sweep or OnDisconnect
// had marked it offline.
func (p *Presence) RecordHeartbeat(deviceID string, at time.Time) Transition {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.stateOf(deviceID)
	wasOnline := st.online
	st.lastSeen = at
	st.online = true
	return Transition{DeviceID: deviceID, Online: true, Changed: !wasOnline}
}

// Sweep marks every device whose last heartbeat is older than the freshness
// threshold offline, and returns one Transition per device that changed
// state on this call. A device already offline is not returned again, so
// repeated Sweeps over a quiet fleet are idempotent.
func (p *Presence) Sweep(now time.Time) []Transition {
	p.mu.Lock()
	defer p.mu.Unlock()

	var transitions []Transition
	for id, st := range p.devices {
		if st.online && now.Sub(st.lastSeen) > p.threshold {
			st.online = false
			transitions = append(transitions, Transition{DeviceID: id, Online: false, Changed: true})
		}
	}
	return transitions
}

// OnConnect records an IoT Core "connected" lifecycle event: the device is
// online as of at. last_seen is refreshed to at so the next Sweep does not
// immediately re-offline a device that reconnected after a long gap. The
// returned Transition.Changed is false if the device was already online.
func (p *Presence) OnConnect(deviceID string, at time.Time) Transition {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.stateOf(deviceID)
	wasOnline := st.online
	st.online = true
	st.lastSeen = at
	return Transition{DeviceID: deviceID, Online: true, Changed: !wasOnline}
}

// OnDisconnect records an IoT Core "disconnected" lifecycle event — the
// fast-path online→offline edge that does not wait for Sweep. The returned
// Transition.Changed is false if the device was already offline. at is
// accepted for symmetry with OnConnect; a disconnect is not a freshness
// signal, so last_seen is left untouched.
func (p *Presence) OnDisconnect(deviceID string, at time.Time) Transition {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.stateOf(deviceID)
	wasOnline := st.online
	st.online = false
	return Transition{DeviceID: deviceID, Online: false, Changed: wasOnline}
}

// LastSeen returns the most recent heartbeat (or connect) time recorded for
// deviceID and whether the device has been seen at all.
func (p *Presence) LastSeen(deviceID string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.devices[deviceID]
	if st == nil {
		return time.Time{}, false
	}
	return st.lastSeen, true
}

// stateOf returns deviceID's state, creating it if absent. Callers hold p.mu.
func (p *Presence) stateOf(deviceID string) *deviceState {
	st := p.devices[deviceID]
	if st == nil {
		st = &deviceState{}
		p.devices[deviceID] = st
	}
	return st
}
