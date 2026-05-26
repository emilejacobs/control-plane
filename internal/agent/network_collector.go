package agent

import (
	"context"
	"time"
)

// networkCollectorTimeout caps the wall-clock budget for the
// tailscale subprocess. The detector path stays well under this on
// every Mac we've measured; the cap exists as a circuit-breaker so
// a hung tailscaled never delays the heartbeat tick.
const networkCollectorTimeout = 2 * time.Second

// NewNetworkCollector returns a telemetry collector that publishes
// lan_ip, tailscale_ip, and tailscale_name as separate keys in the
// heartbeat payload. Each field is OMITTED from the map (not set
// to "") when the underlying detector returned empty — so the wire
// envelope carries only the fields the agent actually has, and
// cp-ingest's conditional UPDATE doesn't overwrite previously
// stored values with NULL when the agent loses tailnet visibility.
//
// Detection runs once per tick (cheap on modern Macs; subsecond
// for both helpers). Caching is a follow-up if it ever shows up in
// profiles.
func NewNetworkCollector(enum InterfaceAddrEnumerator, runner TailscaleStatusRunner) func() map[string]any {
	return func() map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), networkCollectorTimeout)
		defer cancel()

		out := map[string]any{}
		if v := PrimaryRFC1918Addr(enum); v != "" {
			out["lan_ip"] = v
		}
		if v := PrimaryTailscaleAddr(enum); v != "" {
			out["tailscale_ip"] = v
		}
		// ResolveTailscaleName never returns a non-nil error — the
		// cycle 2 contract. We ignore err here for that reason.
		if name, _ := ResolveTailscaleName(ctx, runner); name != "" {
			out["tailscale_name"] = name
		}
		return out
	}
}
