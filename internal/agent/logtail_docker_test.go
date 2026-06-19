package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/logtail"
)

// buildDockerLogsArgs is the docker-CLI argv builder used by the
// production executor. Pinning the args explicitly so a refactor can't
// silently flip flags (e.g., add --timestamps) without flipping this
// expectation too.
func TestBuildDockerLogsArgs(t *testing.T) {
	args := buildDockerLogsArgs("plate-recognizer-stream", 200)
	want := []string{"logs", "--tail", "200", "plate-recognizer-stream"}
	if len(args) != len(want) {
		t.Fatalf("argv length: got %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("argv[%d]: got %q, want %q", i, args[i], want[i])
		}
	}
}

// TestColimaDockerLogsArgv pins the ADR-038 Colima-wrapped argv: the root agent
// reaches the ALPR container in the auto-login user's Colima daemon via
// `launchctl asuser … sudo -u … <dockerBin> --context colima logs …`.
func TestColimaDockerLogsArgv(t *testing.T) {
	got := colimaDockerLogsArgv("501", "uknomi", "/opt/homebrew/bin/docker", "plate-recognizer-stream", 200)
	want := []string{
		"launchctl", "asuser", "501", "sudo", "-u", "uknomi", "/opt/homebrew/bin/docker",
		"--context", "colima", "logs", "--tail", "200", "plate-recognizer-stream",
	}
	if len(got) != len(want) {
		t.Fatalf("argv length: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// init() wires dockerLogsFn to execDockerLogs in production. The fake
// swap in withFakeDockerLogs restores that, so an end-to-end "real
// docker" path is reachable when integration tests need it. This test
// just confirms the var isn't nil after package init.
func TestDockerLogsFnWiredToProductionExecutor(t *testing.T) {
	if dockerLogsFn == nil {
		t.Fatal("dockerLogsFn nil after init; production wiring missing")
	}
}

// White-box tests for TailDocker. Lives in package agent (not
// agent_test) so it can swap the dockerLogsFn seam without exporting
// a setter. Real docker invocation is covered by an opt-in integration
// test gated on a running container — these unit tests stay
// hermetic.

// withFakeDockerLogs swaps dockerLogsFn for the duration of t, restoring
// it on cleanup. Test seam pattern matches handler-side fakeReader.
func withFakeDockerLogs(t *testing.T, fake func(container string, lines int) ([]byte, error)) {
	t.Helper()
	orig := dockerLogsFn
	dockerLogsFn = fake
	t.Cleanup(func() { dockerLogsFn = orig })
}

// Happy path: dockerLogsFn returns a few lines → TailDocker returns
// them verbatim, no truncation. Asserts the seam routes the container
// name + lines through unchanged.
func TestTailDockerHappyPath(t *testing.T) {
	var gotContainer string
	var gotLines int
	withFakeDockerLogs(t, func(container string, lines int) ([]byte, error) {
		gotContainer = container
		gotLines = lines
		return []byte("pr line 1\npr line 2\npr line 3\n"), nil
	})

	resp, err := TailDocker("plate-recognizer-stream", 50, logtail.MaxContentSize)
	if err != nil {
		t.Fatalf("TailDocker: %v", err)
	}
	if gotContainer != "plate-recognizer-stream" {
		t.Errorf("container passed to dockerLogsFn: got %q, want %q", gotContainer, "plate-recognizer-stream")
	}
	if gotLines != 50 {
		t.Errorf("lines passed to dockerLogsFn: got %d, want 50", gotLines)
	}
	if resp.Content != "pr line 1\npr line 2\npr line 3\n" {
		t.Errorf("content: got %q", resp.Content)
	}
	if resp.Truncated {
		t.Error("Truncated: got true, want false on a small response")
	}
}

// Docker invocation error → CodeReadError ValidationError so the
// dashboard surfaces the failure with a stable code (matches the
// CodeReadError contract the file branch already uses for missing
// files).
func TestTailDockerErrorMapsToCodeReadError(t *testing.T) {
	withFakeDockerLogs(t, func(string, int) ([]byte, error) {
		return nil, errors.New("docker daemon not running")
	})

	_, err := TailDocker("plate-recognizer-stream", 50, logtail.MaxContentSize)
	if err == nil {
		t.Fatal("expected error")
	}
	v, ok := logtail.AsValidation(err)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}
	if v.Code != logtail.CodeReadError {
		t.Errorf("code: got %q, want %q", v.Code, logtail.CodeReadError)
	}
	if !strings.Contains(v.Message, "plate-recognizer-stream") {
		t.Errorf("message should include container name: got %q", v.Message)
	}
}

// Content-size cap fires: a single huge line (no newlines) exceeds
// the cap → Truncated=true, TruncatedFrom=lines, content trimmed to
// the most-recent bytes. Mirrors the file branch's truncation
// semantics so the dashboard renders the same banner.
func TestTailDockerTruncatesByContentSize(t *testing.T) {
	const cap = 1024
	// Build content > cap with embedded newlines so the line-realign
	// path runs.
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString(strings.Repeat("x", 20))
		sb.WriteByte('\n')
	}
	huge := sb.String()
	if len(huge) <= cap {
		t.Fatalf("test setup: expected content > cap (%d), got %d", cap, len(huge))
	}
	withFakeDockerLogs(t, func(string, int) ([]byte, error) {
		return []byte(huge), nil
	})

	resp, err := TailDocker("plate-recognizer-stream", 500, cap)
	if err != nil {
		t.Fatalf("TailDocker: %v", err)
	}
	if !resp.Truncated {
		t.Error("Truncated: got false, want true")
	}
	if resp.TruncatedFrom != 500 {
		t.Errorf("TruncatedFrom: got %d, want 500", resp.TruncatedFrom)
	}
	if len(resp.Content) > cap {
		t.Errorf("content size: got %d, want ≤ %d", len(resp.Content), cap)
	}
}
