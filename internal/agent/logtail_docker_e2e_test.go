package agent_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/agent"
	"github.com/emilejacobs/control-plane/internal/envelope"
	protologtail "github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

// End-to-end docker-kind log.tail test (issue #7 acceptance criterion:
// "selecting it returns live container output"). Spins a real
// short-lived container with known stdout, drives a log.tail cmd
// through the dispatcher via the production defaultLogTailReader
// (which calls the real `docker logs`), and asserts the cmd-result
// envelope carries the container's output.
//
// Gated on the docker CLI being on PATH and the daemon being
// reachable — if either fails we skip rather than fail (Linux dev
// boxes / CI without docker get a clean skip; macOS dev with
// Colima/Desktop runs the full path).
func TestAgentLogTailDockerEndToEnd(t *testing.T) {
	requireDocker(t)

	containerName := "uknomi-cp-test-logtail-" + randomSuffix(t)
	knownOutput := "hello from cp test " + randomSuffix(t)

	// Run a one-shot container that prints knownOutput. --rm cleans up
	// on exit; the container is gone by the time we tail logs, BUT
	// `docker logs` against a recently-removed container still works
	// until the daemon GCs it — so we use --rm=false to keep it
	// available for the tail, then clean up at the end of the test.
	runCmd := exec.Command("docker", "run", "--name", containerName,
		"alpine:3.19", "sh", "-c", "echo '"+knownOutput+"'")
	runOut, err := runCmd.CombinedOutput()
	if err != nil {
		t.Skipf("docker run failed (likely image-pull or daemon issue): %v\n%s", err, runOut)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	})

	// Drive the agent with a custom Reader that uses the production
	// default but with our test container in its allow-list. This
	// goes through the real dockerLogsFn — no fakes on this path.
	cert := writeTestCert(t, time.Now().Add(time.Hour))
	tr := newFakeTransport()
	reader := &testContainerReader{containerName: containerName}
	a, err := agent.New(agent.Config{
		CertPath: cert,
		DeviceID: "dev-logtail-docker-e2e",
		Version:  "test",
	}, tr, agent.WithLogTailReader(reader))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	cmd := envelope.Command{
		Type:          "log.tail",
		CorrelationID: "corr-docker-e2e",
		CommandID:     "cmd-docker-e2e",
		Args:          json.RawMessage(`{"log_name":"test-container","lines":50}`),
		IssuedAt:      time.Now(),
	}
	cmdBytes, _ := json.Marshal(cmd)
	tr.deliverTo("devices/dev-logtail-docker-e2e/cmd", cmdBytes)

	results := tr.publishedOn("devices/dev-logtail-docker-e2e/cmd-result")
	if len(results) != 1 {
		t.Fatalf("expected 1 cmd-result, got %d", len(results))
	}
	var result envelope.Result
	if err := json.Unmarshal(results[0], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %+v err=%+v", result, result.Error)
	}
	if result.Type != "log.tail" {
		t.Errorf("Type: got %q, want log.tail", result.Type)
	}
	var resp protologtail.Response
	if err := json.Unmarshal(result.Result, &resp); err != nil {
		t.Fatalf("unmarshal Response: %v", err)
	}
	if !strings.Contains(resp.Content, knownOutput) {
		t.Errorf("docker logs content missing expected line\ngot: %q\nwant substring: %q", resp.Content, knownOutput)
	}
}

// requireDocker skips the test if the docker CLI isn't on PATH or
// the daemon isn't reachable. Mirrors the testcontainers-style gate
// the integration suite uses.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not on PATH — skipping docker-kind log.tail e2e")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon unreachable: %v — skipping e2e", err)
	}
}

// randomSuffix returns a short pseudo-random suffix for container
// names + log content. Uses the test name + time so parallel runs
// don't collide.
func randomSuffix(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().Format("150405.000000")
}

// testContainerReader points a docker-kind entry at a test-spawned
// container. AllowList exposes one entry; Tail delegates to the
// production default reader so the real dockerLogsFn runs.
type testContainerReader struct {
	containerName string
}

func (r *testContainerReader) AllowList() map[string]protologtail.Entry {
	return map[string]protologtail.Entry{
		"test-container": {
			Name:   "test-container",
			Kind:   protologtail.KindDocker,
			Target: r.containerName,
			Label:  "Test container",
		},
	}
}

func (r *testContainerReader) Tail(entry protologtail.Entry, lines int) (protologtail.Response, error) {
	// Delegate to the production fetcher so the real docker CLI runs.
	return agent.TailDocker(entry.Target, lines, protologtail.MaxContentSize)
}
