package agent

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
)

// buildDockerLogsArgs constructs the argv suffix passed to the docker
// CLI for a `log.tail` of a container. Kept pure (no exec) so unit
// tests can pin the exact flags without spawning a process.
//
// Output is concatenated stdout+stderr per docker's default behaviour
// (no --details, no timestamps — matches what `docker logs <c>` shows
// today; operators get container output identical to what they'd see
// over SSH).
func buildDockerLogsArgs(container string, lines int) []string {
	return []string{
		"logs",
		"--tail", strconv.Itoa(lines),
		container,
	}
}

// execDockerLogs runs `docker logs --tail N <container>` and returns
// the captured output (stdout+stderr merged). Errors include the
// captured stderr so the dashboard surfaces useful failure modes
// ("container not found", "docker daemon not running").
//
// Plays the role the file branch's os.Open plays — production wiring
// only. Unit tests inject a fake via dockerLogsFn (see logtail.go).
func execDockerLogs(container string, lines int) ([]byte, error) {
	args := buildDockerLogsArgs(container, lines)
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Concatenate stderr into the error so the dashboard sees the
		// actual docker message ("Error: No such container: foo") rather
		// than a bare exit-code error.
		return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	// docker logs sends stdout and stderr separately; merge so the
	// operator sees the same chronological-ish view they'd get from
	// `docker logs` on a terminal.
	out := append(stdout.Bytes(), stderr.Bytes()...)
	return out, nil
}

func init() {
	// Production wiring: point the seam at the real docker executor.
	// Tests swap it out via withFakeDockerLogs (see logtail_docker_test.go).
	dockerLogsFn = execDockerLogs
}
