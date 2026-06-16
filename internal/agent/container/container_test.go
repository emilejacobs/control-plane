package container_test

import (
	"context"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/container"
)

// fakeRunner records every command and returns canned output, standing in for
// the real launchctl-asuser/sudo-u runner so the Manager's command shaping is
// testable without a Colima VM.
type fakeRunner struct {
	calls  [][]string
	output map[string]string
	err    error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.output[strings.Join(call, " ")]), nil
}

func (f *fakeRunner) ran(sub string) bool {
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), sub) {
			return true
		}
	}
	return false
}

func sampleConfig() container.Config {
	return container.Config{
		StreamDir:     "/usr/local/etc/plate-recognizer/stream",
		ContainerName: "plate-recognizer-stream",
		Image:         "platerecognizer/alpr-stream:arm",
		HostPort:      8050,
	}
}

// StartALPR removes any prior container then (re)creates it with the license +
// token, the host-dir mount, the port mapping, and an unless-stopped restart
// policy — all routed through the user runner (not root).
func TestStartALPR(t *testing.T) {
	fr := &fakeRunner{}
	m := container.New(fr, sampleConfig())

	if err := m.StartALPR(context.Background(), "LICENSE-XYZ", "TOKEN-ABC"); err != nil {
		t.Fatalf("StartALPR: %v", err)
	}

	if !fr.ran("docker rm -f plate-recognizer-stream") {
		t.Errorf("prior container not removed; calls=%v", fr.calls)
	}
	for _, want := range []string{
		"docker run",
		"--restart=unless-stopped",
		"--name plate-recognizer-stream",
		"-v /usr/local/etc/plate-recognizer/stream:/user-data",
		"-e LICENSE_KEY=LICENSE-XYZ",
		"-e TOKEN=TOKEN-ABC",
		"-p 8050:8050",
		"platerecognizer/alpr-stream:arm",
	} {
		if !fr.ran(want) {
			t.Errorf("docker run missing %q; calls=%v", want, fr.calls)
		}
	}
}

// Restart bounces the container (used after config.ini changes).
func TestRestart(t *testing.T) {
	fr := &fakeRunner{}
	m := container.New(fr, sampleConfig())
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !fr.ran("docker restart plate-recognizer-stream") {
		t.Errorf("restart not issued; calls=%v", fr.calls)
	}
}

// Logs returns the container's recent output (the log.tail docker kind).
func TestLogs(t *testing.T) {
	fr := &fakeRunner{output: map[string]string{
		"docker logs --tail 50 plate-recognizer-stream": "plate ABC123\nplate XYZ789\n",
	}}
	m := container.New(fr, sampleConfig())

	out, err := m.Logs(context.Background(), 50)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if !strings.Contains(string(out), "plate ABC123") {
		t.Errorf("logs output: got %q", out)
	}
}
