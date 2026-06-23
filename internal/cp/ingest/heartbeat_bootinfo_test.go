package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
)

// Issue #157: a heartbeat carrying boot_time + shutdown cause is forwarded to
// RecordBootInfo with the boot_time parsed to a time, the cause string, and the
// code as a non-nil pointer (so a real code 0 is distinct from "absent").
func TestPresenceIngesterRecordsBootInfo(t *testing.T) {
	at := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	w := &fakeWriter{}
	ing := NewPresenceIngester(presence.New(), w, fixedClock(at))

	code := 5
	hb := Heartbeat{
		DeviceID:          "dev-1",
		CorrelationID:     "corr-1",
		BootTime:          "2026-06-20T17:14:22Z",
		LastShutdownCause: "clean restart",
		ShutdownCauseCode: &code,
	}
	if err := ing.Handle(context.Background(), hb); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.bootInfoCalls) != 1 {
		t.Fatalf("RecordBootInfo calls: got %d want 1", len(w.bootInfoCalls))
	}
	got := w.bootInfoCalls[0]
	wantBoot := time.Date(2026, 6, 20, 17, 14, 22, 0, time.UTC)
	if got.deviceID != "dev-1" || !got.bootTime.Equal(wantBoot) {
		t.Errorf("call: got deviceID=%q bootTime=%v want dev-1 / %v", got.deviceID, got.bootTime, wantBoot)
	}
	if got.cause != "clean restart" || got.code == nil || *got.code != 5 {
		t.Errorf("cause/code: got %q/%v want clean restart/5", got.cause, got.code)
	}
	if !got.at.Equal(at) {
		t.Errorf("detected_at: got %v want %v (ingest clock)", got.at, at)
	}
}

// A heartbeat from a pre-#157 agent (no boot_time) skips the boot-info path
// entirely — back-compatible.
func TestPresenceIngesterSkipsBootInfoWhenAbsent(t *testing.T) {
	w := &fakeWriter{}
	ing := NewPresenceIngester(presence.New(), w, fixedClock(time.Now()))

	if err := ing.Handle(context.Background(), Heartbeat{DeviceID: "dev-1", CorrelationID: "c"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(w.bootInfoCalls) != 0 {
		t.Errorf("RecordBootInfo should be skipped without a boot_time; got %+v", w.bootInfoCalls)
	}
}

// A malformed boot_time is dropped (logged), not poisoned — the heartbeat is
// otherwise fine and last_seen still lands. Boot info is best-effort.
func TestPresenceIngesterMalformedBootTimeIsNotFatal(t *testing.T) {
	w := &fakeWriter{}
	ing := NewPresenceIngester(presence.New(), w, fixedClock(time.Now()))

	hb := Heartbeat{DeviceID: "dev-1", CorrelationID: "c", BootTime: "not-a-timestamp"}
	if err := ing.Handle(context.Background(), hb); err != nil {
		t.Fatalf("Handle: got %v, want nil (malformed boot_time is non-fatal)", err)
	}
	if len(w.bootInfoCalls) != 0 {
		t.Errorf("RecordBootInfo called with an unparseable boot_time; got %+v", w.bootInfoCalls)
	}
	if len(w.calls) != 1 {
		t.Errorf("last_seen should still be written; got %d UpdateLastSeen calls", len(w.calls))
	}
}

// An unknown device surfaced by the boot-info write is poison (DLQ), matching
// the other writes in Handle.
func TestPresenceIngesterBootInfoUnknownDeviceIsPoison(t *testing.T) {
	w := &fakeWriter{bootInfoErr: registry.ErrDeviceNotFound}
	ing := NewPresenceIngester(presence.New(), w, fixedClock(time.Now()))

	hb := Heartbeat{DeviceID: "dev-1", CorrelationID: "c", BootTime: "2026-06-20T17:14:22Z"}
	err := ing.Handle(context.Background(), hb)
	if !errors.Is(err, sqsconsumer.ErrPoison) {
		t.Errorf("got %v want poison", err)
	}
}
