package healthprobes

// ProbeHostNetPressure is the host TCP/ephemeral-port pressure probe — the
// first continuous-metric probe. The agent reports raw counts; CP scores them
// against tunable thresholds at ingest (so the red/yellow/green line can be
// adjusted fleet-wide without an agent redeploy). Born from the 2026-06-26
// store13/mesa ephemeral-port-exhaustion incident.
const ProbeHostNetPressure = "host_net_pressure"

// HostPressureMetrics is the raw, OS-agnostic payload the agent measures and
// reports in Result.Details. CP reads it back to score the probe.
type HostPressureMetrics struct {
	EphemeralPct       float64 `json:"ephemeral_pct"` // distinct local ephemeral ports in use, as % of the pool
	EphemeralPortsUsed int     `json:"ephemeral_ports_used"`
	PoolSize           int     `json:"pool_size"`
	TimeWait           int     `json:"time_wait"`
	CloseWait          int     `json:"close_wait"`
}

// HostPressureThresholds is the CP-tunable scoring policy for the probe.
type HostPressureThresholds struct {
	EphemeralWarnPct float64 `json:"ephemeral_warn_pct"`
	EphemeralCritPct float64 `json:"ephemeral_crit_pct"`
	CloseWaitWarn    int     `json:"close_wait_warn"`
	CloseWaitCrit    int     `json:"close_wait_crit"`
}

// DefaultHostPressureThresholds are the calibrated defaults (2026-06-26 fleet
// capture: healthy ~0.3% pool, mesa wedged at 82%). 60% crit leaves ~6.5k ports
// of runway before exhaustion.
var DefaultHostPressureThresholds = HostPressureThresholds{
	EphemeralWarnPct: 40,
	EphemeralCritPct: 60,
	CloseWaitWarn:    100,
	CloseWaitCrit:    400,
}

// Details serialises the metrics into the Result.Details map the agent
// reports. Keys are defined here once so the agent (writer) and cp-ingest
// (reader) can't drift.
func (m HostPressureMetrics) Details() map[string]any {
	return map[string]any{
		"ephemeral_pct":        m.EphemeralPct,
		"ephemeral_ports_used": m.EphemeralPortsUsed,
		"pool_size":            m.PoolSize,
		"time_wait":            m.TimeWait,
		"close_wait":           m.CloseWait,
	}
}

// HostPressureFromDetails reads metrics back out of a Result.Details map.
// Numbers survive the JSON/MQTT/SQS hop as float64, so it coerces tolerantly.
func HostPressureFromDetails(d map[string]any) HostPressureMetrics {
	return HostPressureMetrics{
		EphemeralPct:       asFloat(d["ephemeral_pct"]),
		EphemeralPortsUsed: int(asFloat(d["ephemeral_ports_used"])),
		PoolSize:           int(asFloat(d["pool_size"])),
		TimeWait:           int(asFloat(d["time_wait"])),
		CloseWait:          int(asFloat(d["close_wait"])),
	}
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

// EvalHostPressure scores raw metrics against thresholds, returning the probe
// colour and an OS-agnostic state token ("ok" | "elevated" | "critical").
func EvalHostPressure(m HostPressureMetrics, t HostPressureThresholds) (Status, string) {
	// Each signal scores independently; the probe takes the worse of the two.
	rank := level(m.EphemeralPct >= t.EphemeralWarnPct, m.EphemeralPct >= t.EphemeralCritPct)
	if cw := level(m.CloseWait >= t.CloseWaitWarn, m.CloseWait >= t.CloseWaitCrit); cw > rank {
		rank = cw
	}
	switch rank {
	case 2:
		return StatusRed, "critical"
	case 1:
		return StatusYellow, "elevated"
	default:
		return StatusGreen, "ok"
	}
}

// level maps a (warn, crit) pair of threshold crossings to 0/1/2.
func level(warn, crit bool) int {
	switch {
	case crit:
		return 2
	case warn:
		return 1
	default:
		return 0
	}
}
