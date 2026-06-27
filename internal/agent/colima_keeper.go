package agent

import (
	"context"
	"time"

	"github.com/emilejacobs/control-plane/internal/probes"
)

// colimaEnsureInterval is how often the agent re-checks that Colima is up.
// Gentle on purpose: the user LaunchAgent is the primary starter, this is a
// backstop (issue #172), and `colima start` is idempotent — so the interval
// doubles as the rate-limit / back-off, no need for finer-grained throttling.
const colimaEnsureInterval = 5 * time.Minute

// runColimaKeeper ensures the per-user Colima VM stays up (issue #172). It
// checks once immediately — so a device whose LaunchAgent lost the boot race
// recovers within seconds of the agent starting, not a whole interval later —
// then re-checks on a ticker until ctx is cancelled.
func (a *Agent) runColimaKeeper(ctx context.Context, ce probes.ColimaEnsurer, interval time.Duration) {
	ce.EnsureColima(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ce.EnsureColima(ctx)
		}
	}
}
