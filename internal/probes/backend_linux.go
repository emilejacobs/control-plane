//go:build linux

package probes

import (
	"context"
	"log/slog"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// linuxBackend is the deferred Linux probe backend (ADR-007: the
// Pi/Radxa fleet gets a minimal agent and is being consolidated onto
// Macs, so no Linux-specific probe investment yet). It satisfies the
// OS-agnostic Backend interface from day one (ADR-034) by returning no
// probes; the CP-side pipeline treats an empty Report as a no-op. The
// cross-OS check mapping for a future implementation is documented in
// the fleet-health-probes PRD.
type linuxBackend struct{}

// NewSystemBackend returns the no-op Linux backend. expectedLoginUser
// and logger are accepted for signature parity with the darwin backend.
func NewSystemBackend(_ string, _ *slog.Logger) Backend { return linuxBackend{} }

func (linuxBackend) Collect(_ context.Context) []healthprobes.Result { return nil }
