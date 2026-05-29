// Package healthprobes defines the wire types for the per-device
// fleet-health-probes flow (Phase 2, issue #19). The agent's probe
// collector produces a Report; cp-ingest's health-probe handler
// consumes the same shape after the IoT Rule → SQS hop.
//
// Per ADR-034 the probe names and signal vocabulary are OS-agnostic:
// CP never sees launchctl/defaults/kcpassword/system_profiler. The
// per-OS check methods live behind the agent-side probes.Backend
// interface; this package is just the shared shape so a schema change
// is a single edit, not two.
package healthprobes

import "time"

// Status is the agent-decided colour for a probe. The agent owns the
// red/yellow/green scoring (see the PRD Decisions table); CP stores
// and aggregates but does not recompute it.
type Status string

const (
	StatusGreen  Status = "green"
	StatusYellow Status = "yellow"
	StatusRed    Status = "red"
)

// Probe-name constants — OS-agnostic identifiers, shared between the
// agent collector and the cp-ingest handler so a name cannot drift
// between the two sides (ADR-034).
const (
	ProbeAutoLogin                = "auto_login"
	ProbeGUISession               = "gui_session"
	ProbePlateRecognizerContainer = "plate_recognizer_container"
	ProbePlateRecognizerConfig    = "plate_recognizer_config"
	ProbeUSBAudio                 = "usb_audio"
	ProbeWhisperModel             = "whisper_model"
	ProbeBootSanity               = "boot_sanity"
)

// Result is one probe's observation. State is the OS-agnostic signal
// token (e.g. "configured", "missing", "running"); Details carries the
// structured per-probe payload that lands in device_health_probes.details_jsonb.
type Result struct {
	Name    string         `json:"name"`
	Status  Status         `json:"status"`
	State   string         `json:"state"`
	Details map[string]any `json:"details,omitempty"`
}

// Report is the JSON payload published on devices/{id}/health-probes.
type Report struct {
	DeviceID      string    `json:"device_id"`
	CorrelationID string    `json:"correlation_id"`
	ReportedAt    time.Time `json:"reported_at"`
	Probes        []Result  `json:"probes"`
}

// Correlation satisfies sqsconsumer.Correlated on the cp-ingest side.
func (r Report) Correlation() string { return r.CorrelationID }
