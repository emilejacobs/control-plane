package healthprobes

import "testing"

// A device sitting at a tiny fraction of the ephemeral-port pool with a
// handful of CLOSE_WAIT sockets is healthy — green/ok. (Real fleet baseline:
// healthy stores sat at ~0.2-0.3% pool, 4-6 CLOSE_WAIT.)
func TestEvalHostPressure_HealthyIsGreen(t *testing.T) {
	m := HostPressureMetrics{EphemeralPct: 0.3, CloseWait: 6}
	status, state := EvalHostPressure(m, DefaultHostPressureThresholds)
	if status != StatusGreen {
		t.Errorf("status = %q, want %q", status, StatusGreen)
	}
	if state != "ok" {
		t.Errorf("state = %q, want %q", state, "ok")
	}
}

// CLOSE_WAIT is an independent leak signal: a healthy ephemeral pool with a
// CLOSE_WAIT pile still warns/criticals on its own (the transcriber S3 leak
// fingerprint). Overall status is the worse of the two signals.
func TestEvalHostPressure_CloseWaitIsIndependentSignal(t *testing.T) {
	cases := []struct {
		closeWait  int
		wantStatus Status
		wantState  string
	}{
		{99, StatusGreen, "ok"},        // below warn
		{100, StatusYellow, "elevated"}, // warn
		{399, StatusYellow, "elevated"},
		{400, StatusRed, "critical"}, // crit
		{5000, StatusRed, "critical"},
	}
	for _, c := range cases {
		m := HostPressureMetrics{EphemeralPct: 0.3, CloseWait: c.closeWait}
		status, state := EvalHostPressure(m, DefaultHostPressureThresholds)
		if status != c.wantStatus || state != c.wantState {
			t.Errorf("close_wait=%d: got %q/%q, want %q/%q",
				c.closeWait, status, state, c.wantStatus, c.wantState)
		}
	}
}

// Ephemeral usage at or above the crit threshold is red — this is the state
// mesa was in (82%) before its wedge; red fires the proactive alert.
func TestEvalHostPressure_EphemeralCritIsRed(t *testing.T) {
	for _, pct := range []float64{60, 82.4, 99} {
		m := HostPressureMetrics{EphemeralPct: pct, CloseWait: 6}
		status, state := EvalHostPressure(m, DefaultHostPressureThresholds)
		if status != StatusRed {
			t.Errorf("pct=%v: status = %q, want %q", pct, status, StatusRed)
		}
		if state != "critical" {
			t.Errorf("pct=%v: state = %q, want %q", pct, state, "critical")
		}
	}
}

// The agent writes raw metrics into Result.Details and CP reads them back to
// score the probe — so the map keys must round-trip losslessly. (JSON numbers
// arrive back as float64 after the MQTT/SQS hop, which FromDetails must handle.)
func TestHostPressure_DetailsRoundTrip(t *testing.T) {
	m := HostPressureMetrics{
		EphemeralPct: 82.4, EphemeralPortsUsed: 13503, PoolSize: 16384,
		TimeWait: 4557, CloseWait: 6,
	}
	d := m.Details()
	// simulate the JSON hop: every numeric becomes float64
	jsonish := map[string]any{}
	for k, v := range d {
		switch n := v.(type) {
		case int:
			jsonish[k] = float64(n)
		default:
			jsonish[k] = n
		}
	}
	got := HostPressureFromDetails(jsonish)
	if got != m {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, m)
	}
}

// When the two signals disagree, the worse one wins: ephemeral elevated +
// CLOSE_WAIT critical => red overall.
func TestEvalHostPressure_WorseSignalWins(t *testing.T) {
	m := HostPressureMetrics{EphemeralPct: 45 /*yellow*/, CloseWait: 500 /*red*/}
	status, state := EvalHostPressure(m, DefaultHostPressureThresholds)
	if status != StatusRed || state != "critical" {
		t.Errorf("got %q/%q, want red/critical", status, state)
	}
}

// Ephemeral usage at or above the warn threshold but below crit is yellow —
// surfaced on the dashboard but not paged.
func TestEvalHostPressure_EphemeralWarnIsYellow(t *testing.T) {
	for _, pct := range []float64{40, 55, 59.9} {
		m := HostPressureMetrics{EphemeralPct: pct, CloseWait: 6}
		status, state := EvalHostPressure(m, DefaultHostPressureThresholds)
		if status != StatusYellow {
			t.Errorf("pct=%v: status = %q, want %q", pct, status, StatusYellow)
		}
		if state != "elevated" {
			t.Errorf("pct=%v: state = %q, want %q", pct, state, "elevated")
		}
	}
}
