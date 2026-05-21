// Package presence owns device freshness: the 90-second online threshold
// and the in-memory per-device heartbeat state.
//
// Per PRD § Module decomposition, Presence is the single home for the
// online/offline derivation. Issue 07 covers the heartbeat → last-seen path
// only; Sweep, OnConnect, and OnDisconnect (and the injected clock they
// need) arrive with the sweeper in #08.
package presence

import (
	"sync"
	"time"
)

// OnlineThreshold is the freshness window: a device whose last heartbeat is
// within this much of "now" counts as online. 90s is 3× the 30s heartbeat
// interval, tolerating one missed publish without flapping (PRD User Story 9).
const OnlineThreshold = 90 * time.Second

// IsOnline reports whether a device last seen at lastSeen is online as of
// now. A zero lastSeen (never seen) is always offline.
func IsOnline(lastSeen, now time.Time) bool {
	if lastSeen.IsZero() {
		return false
	}
	return now.Sub(lastSeen) <= OnlineThreshold
}

// Transition is the result of RecordHeartbeat: the device's state after the
// heartbeat and whether that heartbeat changed it. In Issue 07 a heartbeat
// only ever drives offline→online; the online→offline edge is the sweeper's
// job (#08).
type Transition struct {
	DeviceID string
	Online   bool
	Changed  bool
}

type deviceState struct {
	lastSeen time.Time
	online   bool
}

// Presence holds in-memory heartbeat state per device. Safe for concurrent
// use: heartbeats for different devices are independent.
type Presence struct {
	mu      sync.Mutex
	devices map[string]*deviceState
}

func New() *Presence {
	return &Presence{devices: make(map[string]*deviceState)}
}

// RecordHeartbeat marks deviceID as seen at at and online. The returned
// Transition reports whether this heartbeat brought the device online —
// true for its first heartbeat (and, once #08 lands, the first after the
// sweeper marks it offline).
func (p *Presence) RecordHeartbeat(deviceID string, at time.Time) Transition {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.devices[deviceID]
	if st == nil {
		st = &deviceState{}
		p.devices[deviceID] = st
	}
	wasOnline := st.online
	st.lastSeen = at
	st.online = true
	return Transition{DeviceID: deviceID, Online: true, Changed: !wasOnline}
}

// LastSeen returns the most recent heartbeat time recorded for deviceID and
// whether the device has been seen at all.
func (p *Presence) LastSeen(deviceID string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.devices[deviceID]
	if st == nil {
		return time.Time{}, false
	}
	return st.lastSeen, true
}
