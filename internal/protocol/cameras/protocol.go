// Package cameras holds the wire types for the cameras inventory
// surface introduced by Phase 2's Edge UI rework (ADR-030 § 1). Both
// the CP-side API handlers and the agent-side cameras.update handler
// depend on this package so the two halves can't drift on what a
// valid camera record looks like.
package cameras

// Camera is the wire shape for one camera row — used in both the API
// responses (`GET /devices/{id}/cameras`, the POST/PUT response body)
// and in the agent's cameras.update cmd payload (full list).
//
// CameraID is server-assigned in the form cam1, cam2, ... per device.
// It is stable for the camera's lifetime and embedded in URLs like
// `/preview/<camera_id>` once the new Edge UI live preview lands
// (issue #4). is_lpr is constrained to at most one true per device
// via a DB partial unique index (ADR-030 § 1).
type Camera struct {
	CameraID string `json:"camera_id"`
	Label    string `json:"label"`
	RtspURL  string `json:"rtsp_url"`
	IsLPR    bool   `json:"is_lpr"`
}
