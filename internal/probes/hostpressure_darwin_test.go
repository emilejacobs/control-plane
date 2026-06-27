//go:build darwin

package probes

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// netstat -an -p tcp sample: 3 TIME_WAIT, 2 CLOSE_WAIT, 1 ESTABLISHED on
// ephemeral local ports, plus a LISTEN and an sshd connection on non-ephemeral
// local ports (which must NOT count toward the ephemeral pool).
const netstatSample = `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)
tcp4       0      0  192.168.13.234.49234   142.251.1.1.80         TIME_WAIT
tcp4       0      0  192.168.13.234.49235   142.251.1.2.80         TIME_WAIT
tcp4       0      0  192.168.13.234.50000   208.111.40.12.80       TIME_WAIT
tcp4       0      0  192.168.13.234.52001   52.217.1.1.443         CLOSE_WAIT
tcp4       0      0  192.168.13.234.52002   52.217.1.2.443         CLOSE_WAIT
tcp4       0      0  192.168.13.234.61000   1.2.3.4.443            ESTABLISHED
tcp4       0      0  192.168.13.234.22      10.0.0.5.51000         ESTABLISHED
tcp4       0      0  *.8050                 *.*                    LISTEN
`

func TestParseHostPressure_Counts(t *testing.T) {
	m := parseHostPressure(netstatSample, 49152, 65535)
	if m.TimeWait != 3 {
		t.Errorf("TimeWait = %d, want 3", m.TimeWait)
	}
	if m.CloseWait != 2 {
		t.Errorf("CloseWait = %d, want 2", m.CloseWait)
	}
	// distinct ephemeral local ports: 49234, 49235, 50000, 52001, 52002, 61000
	// (22 and 8050 are below the range, excluded)
	if m.EphemeralPortsUsed != 6 {
		t.Errorf("EphemeralPortsUsed = %d, want 6", m.EphemeralPortsUsed)
	}
	if m.PoolSize != 16384 {
		t.Errorf("PoolSize = %d, want 16384", m.PoolSize)
	}
	wantPct := 100 * 6.0 / 16384.0
	if m.EphemeralPct < wantPct-0.001 || m.EphemeralPct > wantPct+0.001 {
		t.Errorf("EphemeralPct = %v, want ~%v", m.EphemeralPct, wantPct)
	}
}

func TestProbeHostNetPressure_ReportsRawMetricsAndDefaultStatus(t *testing.T) {
	b := &darwinBackend{
		run: fakeRunner{results: map[string]cmdResult{
			"netstat -an -p tcp":                    {stdout: netstatSample},
			"sysctl -n net.inet.ip.portrange.first": {stdout: "49152\n"},
			"sysctl -n net.inet.ip.portrange.last":  {stdout: "65535\n"},
		}}.run,
	}

	res := b.probeHostNetPressure(context.Background())

	if res.Name != healthprobes.ProbeHostNetPressure {
		t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbeHostNetPressure)
	}
	// Raw metrics land in Details for CP to score.
	got := healthprobes.HostPressureFromDetails(res.Details)
	if got.TimeWait != 3 || got.CloseWait != 2 || got.EphemeralPortsUsed != 6 || got.PoolSize != 16384 {
		t.Errorf("Details metrics = %+v, want tw=3 cw=2 used=6 pool=16384", got)
	}
	// Agent stamps a default-threshold status for self-consistency (CP may
	// override at ingest). This sample is well under warn → green/ok.
	if res.Status != healthprobes.StatusGreen || res.State != "ok" {
		t.Errorf("status/state = %q/%q, want green/ok", res.Status, res.State)
	}
}
