package agent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	osuser "os/user"
	"strconv"
)

// Colima runtime config (ADR-038). When set (via EnableColimaDocker from the
// agent main on a device with an auto-login user), the docker log executor
// reaches the per-user Colima daemon first via launchctl asuser, falling back
// to root Docker Desktop for not-yet-migrated devices.
var (
	colimaLogsUser string
	colimaLogsUID  string
	colimaLogsBin  string
)

// EnableColimaDocker configures the log.tail docker executor to reach the
// auto-login user's Colima daemon. No-op (legacy root docker) when user is empty
// or can't be resolved.
func EnableColimaDocker(user string) {
	if user == "" {
		return
	}
	u, err := osuser.Lookup(user)
	if err != nil {
		return
	}
	colimaLogsUser = user
	colimaLogsUID = u.Uid
	colimaLogsBin = dockerBinPath()
}

// dockerBinPath resolves the docker CLI by absolute path — under
// `launchctl asuser … sudo -u …` the Homebrew prefix isn't on sudo's PATH, and
// it isn't on the root LaunchDaemon's PATH either. Prefer brew (Colima) docker.
func dockerBinPath() string {
	for _, p := range []string{"/opt/homebrew/bin/docker", "/usr/local/bin/docker"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "docker"
}

// colimaDockerLogsArgv builds the asuser-wrapped argv that tails a container's
// logs in the user's Colima session (ADR-038 §2).
func colimaDockerLogsArgv(uid, user, dockerBin, container string, lines int) []string {
	base := []string{"launchctl", "asuser", uid, "sudo", "-u", user, dockerBin, "--context", "colima"}
	return append(base, buildDockerLogsArgs(container, lines)...)
}

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
	// Colima first (post-migration): tail via the user's Colima daemon. On any
	// error (not migrated / colima context absent) fall back to root Docker
	// Desktop — mixed-fleet safe during the rollout.
	if colimaLogsUser != "" && colimaLogsUID != "" && colimaLogsBin != "" {
		argv := colimaDockerLogsArgv(colimaLogsUID, colimaLogsUser, colimaLogsBin, container, lines)
		if out, err := runDockerLogs(argv[0], argv[1:]); err == nil {
			return out, nil
		}
	}
	return runDockerLogs("docker", buildDockerLogsArgs(container, lines))
}

// runDockerLogs executes the docker-logs argv and merges stdout+stderr, lifting
// docker's stderr into the error so the dashboard surfaces real failure modes.
func runDockerLogs(name string, args []string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return append(stdout.Bytes(), stderr.Bytes()...), nil
}

func init() {
	// Production wiring: point the seam at the real docker executor.
	// Tests swap it out via withFakeDockerLogs (see logtail_docker_test.go).
	dockerLogsFn = execDockerLogs
}
