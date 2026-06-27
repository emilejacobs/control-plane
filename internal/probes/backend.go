// Package probes implements the agent-side fleet-health probes (Phase 2,
// issue #19). Per ADR-034 the probe names and signal vocabulary are
// OS-agnostic; the per-OS check methods live behind the Backend
// interface, mirroring internal/service. The darwin backend ships in
// slice 1 (what the Mac fleet runs); the linux backend is deferred per
// ADR-007 but the interface is OS-agnostic from day one.
package probes

import (
	"context"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// Backend collects the system-state health probes for one device. The
// agent runs Collect on the probe-report cadence and hands the results
// to the telemetry publisher.
type Backend interface {
	Collect(ctx context.Context) []healthprobes.Result
}

// ColimaEnsurer is implemented by backends that can (re)start the per-user
// Colima VM when it has stopped (issue #172). The darwin backend implements
// it; other OSes don't, so the agent's keeper loop simply never runs there —
// the OS split falls out of a type assertion, no extra wiring.
type ColimaEnsurer interface {
	EnsureColima(ctx context.Context)
}
