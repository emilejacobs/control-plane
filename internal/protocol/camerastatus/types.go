// Package camerastatus defines the wire types for the per-device
// camera-status reporting flow (#113, PRD #111 Camera observability).
// The agent's telemetry.CameraStatusCollector probes each configured
// camera's RTSP reachability and produces a Report; cp-ingest's
// camera-status handler consumes the same shape after the IoT Rule →
// SQS hop.
//
// Like servicestatus, this package is deliberately tiny — just the
// types. Behaviour lives on the producer (internal/telemetry) and
// consumer (internal/cp/ingest) sides. Sharing the struct here keeps
// both halves in lockstep.
package camerastatus

import "time"

// Status values the agent reports per camera. The agent only ever
// reports a determined online/offline (debounced); the CP-side
// "unknown" default is owned by the device_cameras row before the
// first report lands, never sent on the wire.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
)

// Report is the JSON payload published on devices/{id}/camera-status.
type Report struct {
	DeviceID      string        `json:"device_id"`
	CorrelationID string        `json:"correlation_id"`
	ReportedAt    time.Time     `json:"reported_at"`
	Cameras       []CameraState `json:"cameras"`
}

// Correlation satisfies sqsconsumer.Correlated on the cp-ingest side,
// keeping the wire shape and the consumer contract in one place.
func (r Report) Correlation() string { return r.CorrelationID }

// CameraState is the per-camera entry in Report.Cameras. CameraID is
// the CP-assigned id (cam1, cam2, …) carried through from the local
// cameras.json; Status is the debounced online/offline the collector
// determined this tick.
type CameraState struct {
	CameraID string `json:"camera_id"`
	Status   string `json:"status"`
}
