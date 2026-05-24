package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/service"
	"github.com/emilejacobs/control-plane/internal/telemetry"
)

func TestServiceStatusCollectorTracerBullet(t *testing.T) {
	now := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	backend := &service.Fake{States: map[string]service.State{
		"com.uknomi.edge-ui": service.StateRunning,
		"nginx":              service.StateRunning,
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-bbe0540c",
		AllowList: []string{"com.uknomi.edge-ui", "nginx"},
		Now:       func() time.Time { return now },
	}

	report := c.Collect(context.Background())

	if report.DeviceID != "dev-bbe0540c" {
		t.Errorf("DeviceID: got %q, want %q", report.DeviceID, "dev-bbe0540c")
	}
	if report.CorrelationID == "" {
		t.Error("CorrelationID is empty; expected a non-empty value")
	}
	if !report.ReportedAt.Equal(now) {
		t.Errorf("ReportedAt: got %v, want %v", report.ReportedAt, now)
	}
	if len(report.Services) != 2 {
		t.Fatalf("Services: got %d entries, want 2", len(report.Services))
	}

	byName := map[string]telemetry.ServiceState{}
	for _, s := range report.Services {
		byName[s.Name] = s
	}
	for _, name := range []string{"com.uknomi.edge-ui", "nginx"} {
		s, ok := byName[name]
		if !ok {
			t.Errorf("missing service entry: %q", name)
			continue
		}
		if s.State != service.StateRunning {
			t.Errorf("%s State: got %q, want %q", name, s.State, service.StateRunning)
		}
	}
}

// A service the backend doesn't know about must still appear in the
// report so the dashboard can show the gap (rather than silently
// dropping the entry). State = "unknown" is the contract; cp-ingest
// persists it as-is and the alarm wiring treats unknown as not-failed.
func TestServiceStatusCollectorReportsUnknownForNotFound(t *testing.T) {
	now := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	backend := &service.Fake{States: map[string]service.State{
		"com.uknomi.edge-ui": service.StateRunning,
		// "nginx" deliberately absent → Fake returns ErrNotFound
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-test",
		AllowList: []string{"com.uknomi.edge-ui", "nginx"},
		Now:       func() time.Time { return now },
	}

	report := c.Collect(context.Background())

	byName := map[string]telemetry.ServiceState{}
	for _, s := range report.Services {
		byName[s.Name] = s
	}

	if got := byName["com.uknomi.edge-ui"].State; got != service.StateRunning {
		t.Errorf("edge-ui State: got %q, want %q", got, service.StateRunning)
	}
	if got := byName["nginx"].State; got != service.StateUnknown {
		t.Errorf("nginx State: got %q, want %q", got, service.StateUnknown)
	}
}

// When the observed state hasn't changed between two Collect calls,
// StateSince must remain at the original observation time. The
// dashboard renders "running since N hours" off this value; if it
// reset every tick, every service would always read "running since 5 min".
func TestServiceStatusCollectorStateSinceStableAcrossCalls(t *testing.T) {
	first := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)

	tick := first
	backend := &service.Fake{States: map[string]service.State{
		"nginx": service.StateRunning,
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-test",
		AllowList: []string{"nginx"},
		Now:       func() time.Time { return tick },
	}

	r1 := c.Collect(context.Background())
	tick = second
	r2 := c.Collect(context.Background())

	if got := r1.Services[0].StateSince; !got.Equal(first) {
		t.Fatalf("r1 StateSince: got %v, want %v", got, first)
	}
	if got := r2.Services[0].StateSince; !got.Equal(first) {
		t.Errorf("r2 StateSince should still be the original observation time; got %v, want %v", got, first)
	}
}

// When the observed state changes between two Collect calls, StateSince
// must advance to the second call's time. This is the converse of the
// stability test — together they pin the "since" semantics.
func TestServiceStatusCollectorStateSinceUpdatesOnTransition(t *testing.T) {
	first := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)

	tick := first
	backend := &service.Fake{States: map[string]service.State{
		"nginx": service.StateRunning,
	}}

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-test",
		AllowList: []string{"nginx"},
		Now:       func() time.Time { return tick },
	}

	_ = c.Collect(context.Background())

	// Service transitions Running → Stopped between calls.
	backend.States["nginx"] = service.StateStopped
	tick = second

	r2 := c.Collect(context.Background())

	if got := r2.Services[0].State; got != service.StateStopped {
		t.Fatalf("r2 State: got %q, want %q", got, service.StateStopped)
	}
	if got := r2.Services[0].StateSince; !got.Equal(second) {
		t.Errorf("r2 StateSince should advance to the transition time; got %v, want %v", got, second)
	}
}

// An empty AllowList must still produce a valid Report (with an empty
// Services slice). The publisher loop will still tick; cp-ingest will
// still UPSERT zero rows. The fleet-wide query "which devices have no
// services reporting" then correctly identifies misconfigured agents
// rather than dropping them silently.
func TestServiceStatusCollectorEmptyAllowListProducesEmptyReport(t *testing.T) {
	now := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	c := &telemetry.ServiceStatusCollector{
		Backend:   &service.Fake{},
		DeviceID:  "dev-test",
		AllowList: nil,
		Now:       func() time.Time { return now },
	}

	report := c.Collect(context.Background())

	if report.DeviceID != "dev-test" {
		t.Errorf("DeviceID: got %q, want %q", report.DeviceID, "dev-test")
	}
	if report.CorrelationID == "" {
		t.Error("CorrelationID empty; expected a non-empty value even for empty AllowList")
	}
	if !report.ReportedAt.Equal(now) {
		t.Errorf("ReportedAt: got %v, want %v", report.ReportedAt, now)
	}
	if report.Services == nil {
		t.Error("Services is nil; want an empty (but non-nil) slice for JSON marshal stability")
	}
	if len(report.Services) != 0 {
		t.Errorf("Services: got %d entries, want 0", len(report.Services))
	}
}

// A non-ErrNotFound error from Backend.Status (e.g. launchctl returned
// a permission error, or the systemd socket is unreachable) must:
//   - report State=unknown, not crash the collection;
//   - emit a slog warning so operators can see the underlying error.
//
// ErrNotFound is the *expected* "service isn't loaded" case and stays
// quiet (verified by the absence of a log line for the missing service
// in the existing #2 test).
func TestServiceStatusCollectorTransientErrorLogsAndReturnsUnknown(t *testing.T) {
	now := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	transientErr := errors.New("launchctl: permission denied")
	backend := &service.Fake{
		States: map[string]service.State{
			"com.uknomi.edge-ui": service.StateRunning,
		},
		StatusErrors: map[string]error{
			"nginx": transientErr,
		},
	}

	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	c := &telemetry.ServiceStatusCollector{
		Backend:   backend,
		DeviceID:  "dev-test",
		AllowList: []string{"com.uknomi.edge-ui", "nginx"},
		Now:       func() time.Time { return now },
		Logger:    logger,
	}

	report := c.Collect(context.Background())

	byName := map[string]telemetry.ServiceState{}
	for _, s := range report.Services {
		byName[s.Name] = s
	}
	if got := byName["com.uknomi.edge-ui"].State; got != service.StateRunning {
		t.Errorf("edge-ui State: got %q, want %q (transient error on a sibling must not poison this one)", got, service.StateRunning)
	}
	if got := byName["nginx"].State; got != service.StateUnknown {
		t.Errorf("nginx State: got %q, want %q", got, service.StateUnknown)
	}

	if !bytes.Contains(logBuf.Bytes(), []byte("permission denied")) {
		t.Errorf("expected log to mention the underlying error; got: %s", logBuf.String())
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("nginx")) {
		t.Errorf("expected log to identify the failing service by name; got: %s", logBuf.String())
	}
}

// --- ServiceStatusPublisher ------------------------------------------------

// recordingTransport is a thread-safe Transport that captures every
// publish for later inspection. Mirrors the heartbeat publisher's
// fakeTransport pattern.
type recordingTransport struct {
	mu        sync.Mutex
	published map[string][][]byte
	gotOne    chan struct{}
}

func newRecordingTransport() *recordingTransport {
	return &recordingTransport{
		published: make(map[string][][]byte),
		gotOne:    make(chan struct{}, 1),
	}
}

func (t *recordingTransport) Publish(topic string, payload []byte) error {
	t.mu.Lock()
	t.published[topic] = append(t.published[topic], payload)
	t.mu.Unlock()
	select {
	case t.gotOne <- struct{}{}:
	default:
	}
	return nil
}

func (t *recordingTransport) snapshot(topic string) [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.published[topic]))
	copy(out, t.published[topic])
	return out
}

// Tracer bullet for the publisher loop: given a Collect func that
// returns a fixed Report, the publisher publishes the JSON-marshalled
// Report on devices/{id}/service-status within one tick.
func TestServiceStatusPublisherEmitsOneTick(t *testing.T) {
	tr := newRecordingTransport()
	now := time.Date(2026, 5, 24, 18, 0, 0, 0, time.UTC)
	stubReport := telemetry.Report{
		DeviceID:      "dev-bbe0540c",
		CorrelationID: "corr-abc",
		ReportedAt:    now,
		Services: []telemetry.ServiceState{
			{Name: "nginx", State: service.StateRunning, StateSince: now},
		},
	}

	p := &telemetry.ServiceStatusPublisher{
		Interval:  5 * time.Millisecond,
		DeviceID:  "dev-bbe0540c",
		Collect:   func(_ context.Context) telemetry.Report { return stubReport },
		Transport: tr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	select {
	case <-tr.gotOne:
	case <-time.After(time.Second):
		t.Fatal("no publish within 1s")
	}
	cancel()
	<-done

	publishes := tr.snapshot("devices/dev-bbe0540c/service-status")
	if len(publishes) == 0 {
		t.Fatalf("expected at least one publish on devices/dev-bbe0540c/service-status; got payloads on: %v", topicsIn(tr))
	}

	var got telemetry.Report
	if err := json.Unmarshal(publishes[0], &got); err != nil {
		t.Fatalf("payload not a valid Report JSON: %v\nraw: %s", err, publishes[0])
	}
	if got.DeviceID != stubReport.DeviceID || got.CorrelationID != stubReport.CorrelationID {
		t.Errorf("payload identity mismatch: got %+v, want %+v", got, stubReport)
	}
	if len(got.Services) != 1 || got.Services[0].Name != "nginx" {
		t.Errorf("Services round-trip lost data: got %+v", got.Services)
	}
}

func topicsIn(t *recordingTransport) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.published))
	for k := range t.published {
		out = append(out, k)
	}
	return out
}
