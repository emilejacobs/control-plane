//go:build darwin

package probes

import (
	"context"
	"strconv"
	"strings"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// ephemeral range fallback (macOS default) when sysctl can't be read.
const (
	defaultEphemeralLo = 49152
	defaultEphemeralHi = 65535
)

// probeHostNetPressure measures TCP/ephemeral-port pressure (the failure mode
// behind the store13/mesa incident) and reports raw metrics in Details. The
// agent stamps a default-threshold status for self-consistency; cp-ingest may
// override it from the CP-tunable thresholds.
func (b *darwinBackend) probeHostNetPressure(ctx context.Context) healthprobes.Result {
	lo, hi := b.ephemeralRange(ctx)
	out, _, _ := b.run(ctx, "netstat", "-an", "-p", "tcp")
	m := parseHostPressure(string(out), lo, hi)
	status, state := healthprobes.EvalHostPressure(m, healthprobes.DefaultHostPressureThresholds)
	return healthprobes.Result{
		Name:    healthprobes.ProbeHostNetPressure,
		Status:  status,
		State:   state,
		Details: m.Details(),
	}
}

// ephemeralRange reads the host's ephemeral port range via sysctl, falling
// back to the macOS default if either bound can't be read.
func (b *darwinBackend) ephemeralRange(ctx context.Context) (int, int) {
	lo := b.sysctlInt(ctx, "net.inet.ip.portrange.first", defaultEphemeralLo)
	hi := b.sysctlInt(ctx, "net.inet.ip.portrange.last", defaultEphemeralHi)
	if lo <= 0 || hi <= lo {
		return defaultEphemeralLo, defaultEphemeralHi
	}
	return lo, hi
}

func (b *darwinBackend) sysctlInt(ctx context.Context, key string, fallback int) int {
	out, _, err := b.run(ctx, "sysctl", "-n", key)
	if err != nil {
		return fallback
	}
	if n, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
		return n
	}
	return fallback
}

// parseHostPressure extracts TCP/ephemeral-port pressure from `netstat -an -p tcp`
// output. lo/hi bound the ephemeral port range; a local port inside it counts
// once toward the pool regardless of connection state, so the % reflects true
// port consumption (the thing that exhausts and wedges the host).
func parseHostPressure(out string, lo, hi int) healthprobes.HostPressureMetrics {
	m := healthprobes.HostPressureMetrics{PoolSize: hi - lo + 1}
	seen := make(map[int]struct{})
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 5 || !strings.HasPrefix(f[0], "tcp") {
			continue
		}
		switch f[len(f)-1] {
		case "TIME_WAIT":
			m.TimeWait++
		case "CLOSE_WAIT":
			m.CloseWait++
		}
		// Local Address is column 4 (index 3), formatted ip.port — port is
		// the segment after the final dot.
		local := f[3]
		if dot := strings.LastIndex(local, "."); dot >= 0 {
			if p, err := strconv.Atoi(local[dot+1:]); err == nil && p >= lo && p <= hi {
				seen[p] = struct{}{}
			}
		}
	}
	m.EphemeralPortsUsed = len(seen)
	if m.PoolSize > 0 {
		m.EphemeralPct = 100 * float64(m.EphemeralPortsUsed) / float64(m.PoolSize)
	}
	return m
}
