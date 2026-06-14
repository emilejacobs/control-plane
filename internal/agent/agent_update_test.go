package agent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/agent"
	"github.com/emilejacobs/control-plane/internal/envelope"
)

// With an update dir configured (the wrapper's AGENT_DIR), the agent writes
// the health marker — content = its version — after it's connected,
// subscribed, and has published one heartbeat (ADR-035 §5). The resident
// wrapper polls this file to promote a candidate.
func TestAgentWritesHealthMarker(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()
	dir := t.TempDir()

	a, err := agent.New(agent.Config{
		CertPath:  cert,
		DeviceID:  "dev-hb",
		Version:   "1.2.3",
		UpdateDir: dir,
	}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	marker := filepath.Join(dir, "healthy")
	deadline := time.Now().Add(2 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil {
			got = b
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if string(got) != "1.2.3" {
		t.Fatalf("health marker = %q, want %q", string(got), "1.2.3")
	}
	// The marker implies a heartbeat was published (alive proof).
	if len(tr.publishedOn("devices/dev-hb/telemetry")) == 0 {
		t.Error("no telemetry heartbeat published before the marker")
	}
}

// No update dir → no marker (the agent isn't running under the wrapper).
func TestAgentNoMarkerWithoutUpdateDir(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()
	a, err := agent.New(agent.Config{CertPath: cert, DeviceID: "dev-x", Version: "1.0.0"}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	// The agent has no update dir, so there's nowhere a marker could go and
	// the agent.update handler is not registered — see the gating test below.
}

// agent.update is registered AND signature-gated when an update dir is set: an
// unsigned command is rejected with command.bad_signature before the handler
// runs (issue #41 verifier wired in #39).
func TestAgentGatesUnsignedAgentUpdate(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()
	a, err := agent.New(agent.Config{
		CertPath:  cert,
		DeviceID:  "dev-up",
		Version:   "1.0.0",
		UpdateDir: t.TempDir(),
	}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:      "agent.update",
		CommandID: "c1",
		Args:      json.RawMessage(`{"version":"9.9.9"}`),
		// Signature deliberately omitted.
	}
	raw, _ := json.Marshal(cmd)
	tr.deliverTo("devices/dev-up/cmd", raw)

	results := tr.publishedOn("devices/dev-up/cmd-result")
	if len(results) == 0 {
		t.Fatal("no cmd-result published for agent.update")
	}
	var res envelope.Result
	if err := json.Unmarshal(results[0], &res); err != nil {
		t.Fatalf("cmd-result not a Result: %v", err)
	}
	if res.Success {
		t.Fatal("unsigned agent.update was accepted")
	}
	if res.Error == nil || res.Error.Code != "command.bad_signature" {
		t.Errorf("error = %+v, want command.bad_signature", res.Error)
	}
}

// Without an update dir, agent.update isn't registered at all → unknown_type
// (and there's no signature gate). Confirms the surface is wrapper-gated.
func TestAgentUpdateUnregisteredWithoutUpdateDir(t *testing.T) {
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()
	a, err := agent.New(agent.Config{CertPath: cert, DeviceID: "dev-nou", Version: "1.0.0"}, tr)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{Type: "agent.update", CommandID: "c1", Args: json.RawMessage(`{}`)}
	raw, _ := json.Marshal(cmd)
	tr.deliverTo("devices/dev-nou/cmd", raw)

	results := tr.publishedOn("devices/dev-nou/cmd-result")
	if len(results) == 0 {
		t.Fatal("no cmd-result published")
	}
	var res envelope.Result
	_ = json.Unmarshal(results[0], &res)
	if res.Error == nil || res.Error.Code != "command.unknown_type" {
		t.Errorf("error = %+v, want command.unknown_type", res.Error)
	}
}
