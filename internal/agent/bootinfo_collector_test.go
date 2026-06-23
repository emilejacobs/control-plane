package agent

import (
	"testing"
	"time"
)

// The boot-info collector emits the cached boot_time (RFC3339, UTC) plus the
// shutdown cause + raw code when present. The platform read sits behind a seam;
// this drives the collector with a fixture BootInfo (no device).
func TestBootInfoCollectorEmitsAllFields(t *testing.T) {
	info := BootInfo{
		BootTime:          time.Date(2026, 6, 20, 10, 14, 22, 0, time.FixedZone("PDT", -7*3600)),
		ShutdownCause:     "clean restart",
		ShutdownCauseCode: 5,
		HasShutdownCause:  true,
	}
	out := newBootInfoCollector(info, true)()

	if got := out["boot_time"]; got != "2026-06-20T17:14:22Z" {
		t.Errorf("boot_time: got %v want 2026-06-20T17:14:22Z (UTC RFC3339)", got)
	}
	if got := out["last_shutdown_cause"]; got != "clean restart" {
		t.Errorf("last_shutdown_cause: got %v want clean restart", got)
	}
	if got := out["shutdown_cause_code"]; got != 5 {
		t.Errorf("shutdown_cause_code: got %v want 5", got)
	}
}

// When the shutdown cause couldn't be read, boot_time still flows but the
// cause keys are omitted (not emitted empty) — cp-ingest's conditional write
// leaves the stored cause untouched.
func TestBootInfoCollectorOmitsCauseWhenAbsent(t *testing.T) {
	info := BootInfo{
		BootTime:         time.Date(2026, 6, 20, 17, 14, 22, 0, time.UTC),
		HasShutdownCause: false,
	}
	out := newBootInfoCollector(info, true)()

	if _, ok := out["boot_time"]; !ok {
		t.Error("boot_time should be present even without a shutdown cause")
	}
	if _, ok := out["last_shutdown_cause"]; ok {
		t.Error("last_shutdown_cause should be omitted when HasShutdownCause is false")
	}
	if _, ok := out["shutdown_cause_code"]; ok {
		t.Error("shutdown_cause_code should be omitted when HasShutdownCause is false")
	}
}

// On a platform where boot info can't be read (non-macOS, or a read failure),
// the collector emits nothing — older/other agents stay back-compatible and
// cp-ingest skips the boot-info path entirely.
func TestBootInfoCollectorEmitsNothingWhenUnavailable(t *testing.T) {
	out := newBootInfoCollector(BootInfo{}, false)()
	if len(out) != 0 {
		t.Errorf("expected empty map when boot info unavailable; got %v", out)
	}
}
