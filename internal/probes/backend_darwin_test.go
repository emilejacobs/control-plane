//go:build darwin

package probes

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// fakeRunner stages canned stdout/stderr/exit per command so probe
// methods can be exercised without shelling out to real macOS tools.
// Commands are keyed by name + space-joined args.
type fakeRunner struct {
	results map[string]cmdResult
}

type cmdResult struct {
	stdout string
	stderr string
	err    error
}

func (f fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	key := name
	for _, a := range args {
		key += " " + a
	}
	r, ok := f.results[key]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	return []byte(r.stdout), []byte(r.stderr), r.err
}

// fakeStat stages canned file metadata per path.
func fakeStat(files map[string]fileStat) statFunc {
	return func(path string) (fileStat, error) {
		fs, ok := files[path]
		if !ok {
			return fileStat{}, os.ErrNotExist
		}
		return fs, nil
	}
}

func TestProbeAutoLoginConfigured(t *testing.T) {
	b := &darwinBackend{
		run: fakeRunner{results: map[string]cmdResult{
			"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "uknomi\n"},
		}}.run,
		stat: fakeStat(map[string]fileStat{
			"/etc/kcpassword": {Mode: 0o600, UID: 0, GID: 0},
		}),
		expectedLoginUser: "uknomi",
	}

	res := b.probeAutoLogin(context.Background())

	if res.Name != healthprobes.ProbeAutoLogin {
		t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbeAutoLogin)
	}
	if res.State != "configured" {
		t.Errorf("State = %q, want %q", res.State, "configured")
	}
	if res.Status != healthprobes.StatusGreen {
		t.Errorf("Status = %q, want %q", res.Status, healthprobes.StatusGreen)
	}
}

func TestProbeAutoLoginMissing(t *testing.T) {
	cases := map[string]struct {
		runResults map[string]cmdResult
		files      map[string]fileStat
	}{
		"autoLoginUser key absent": {
			// `defaults read` exits non-zero when the key is unset.
			runResults: map[string]cmdResult{},
			files:      map[string]fileStat{"/etc/kcpassword": {Mode: 0o600}},
		},
		"autoLoginUser is a different user": {
			runResults: map[string]cmdResult{
				"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "admin\n"},
			},
			files: map[string]fileStat{"/etc/kcpassword": {Mode: 0o600}},
		},
		"kcpassword absent (the decay failure)": {
			runResults: map[string]cmdResult{
				"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "uknomi\n"},
			},
			files: map[string]fileStat{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := &darwinBackend{
				run:               fakeRunner{results: tc.runResults}.run,
				stat:              fakeStat(tc.files),
				expectedLoginUser: "uknomi",
			}
			res := b.probeAutoLogin(context.Background())
			if res.State != "missing" {
				t.Errorf("State = %q, want %q", res.State, "missing")
			}
			if res.Status != healthprobes.StatusRed {
				t.Errorf("Status = %q, want red", res.Status)
			}
		})
	}
}

// fakeRead stages canned file contents per path.
func fakeRead(files map[string][]byte) fileReadFunc {
	return func(path string) ([]byte, error) {
		b, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return b, nil
	}
}

func TestProbePlateRecognizerConfig(t *testing.T) {
	t.Run("present reports sha256 and size", func(t *testing.T) {
		content := []byte("[stream]\nurl = rtsp://cam\n")
		b := &darwinBackend{
			readFile: fakeRead(map[string][]byte{
				"/usr/local/etc/plate-recognizer/stream/config.ini": content,
			}),
		}
		res := b.probePlateRecognizerConfig(context.Background())
		if res.Name != healthprobes.ProbePlateRecognizerConfig {
			t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbePlateRecognizerConfig)
		}
		if res.State != "present" {
			t.Errorf("State = %q, want present", res.State)
		}
		if res.Status != healthprobes.StatusGreen {
			t.Errorf("Status = %q, want green", res.Status)
		}
		sha, _ := res.Details["sha256"].(string)
		if len(sha) != 64 {
			t.Errorf("Details[sha256] = %q, want 64-char hex digest", sha)
		}
		if got, _ := res.Details["size_bytes"].(int); got != len(content) {
			t.Errorf("Details[size_bytes] = %v, want %d", res.Details["size_bytes"], len(content))
		}
	})

	t.Run("missing", func(t *testing.T) {
		b := &darwinBackend{readFile: fakeRead(map[string][]byte{})}
		res := b.probePlateRecognizerConfig(context.Background())
		if res.State != "missing" {
			t.Errorf("State = %q, want missing", res.State)
		}
		if res.Status != healthprobes.StatusRed {
			t.Errorf("Status = %q, want red", res.Status)
		}
	})
}

func TestParseWhisperFilename(t *testing.T) {
	cases := map[string]struct {
		wantVariant string
		wantQuant   string
	}{
		"ggml-medium.en-q5_0.bin": {"medium.en", "q5_0"},
		"ggml-small.en.bin":       {"small.en", ""},
		"ggml-large-v3-q8_0.bin":  {"large-v3", "q8_0"},
		"ggml-large-v3.bin":       {"large-v3", ""},
		"ggml-base.en-f16.bin":    {"base.en", "f16"},
	}
	for fname, want := range cases {
		t.Run(fname, func(t *testing.T) {
			variant, quant := parseWhisperFilename(fname)
			if variant != want.wantVariant {
				t.Errorf("variant = %q, want %q", variant, want.wantVariant)
			}
			if quant != want.wantQuant {
				t.Errorf("quant = %q, want %q", quant, want.wantQuant)
			}
		})
	}
}

func fakeGlob(matches map[string][]string) globFunc {
	return func(pattern string) ([]string, error) { return matches[pattern], nil }
}

func TestProbeWhisperModel(t *testing.T) {
	const dir = "/usr/local/etc/uknomi/whisper-models/*.bin"
	const fileMedium = "/usr/local/etc/uknomi/whisper-models/ggml-medium.en-q5_0.bin"
	const fileSmall = "/usr/local/etc/uknomi/whisper-models/ggml-small.en.bin"

	t.Run("present reports variant, quantization, size", func(t *testing.T) {
		b := &darwinBackend{
			glob: fakeGlob(map[string][]string{dir: {fileMedium}}),
			stat: fakeStat(map[string]fileStat{fileMedium: {Size: 539 * 1024 * 1024}}),
		}
		res := b.probeWhisperModel(context.Background())
		if res.Name != healthprobes.ProbeWhisperModel {
			t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbeWhisperModel)
		}
		if res.State != "present" || res.Status != healthprobes.StatusGreen {
			t.Fatalf("got state=%q status=%q, want present/green", res.State, res.Status)
		}
		if res.Details["variant"] != "medium.en" {
			t.Errorf("variant = %v, want medium.en", res.Details["variant"])
		}
		if res.Details["quantization"] != "q5_0" {
			t.Errorf("quantization = %v, want q5_0", res.Details["quantization"])
		}
		if res.Details["size_mb"] != 539 {
			t.Errorf("size_mb = %v, want 539", res.Details["size_mb"])
		}
	})

	t.Run("missing when no files", func(t *testing.T) {
		b := &darwinBackend{glob: fakeGlob(map[string][]string{dir: {}}), stat: fakeStat(nil)}
		res := b.probeWhisperModel(context.Background())
		if res.State != "missing" || res.Status != healthprobes.StatusRed {
			t.Errorf("got state=%q status=%q, want missing/red", res.State, res.Status)
		}
	})

	t.Run("multiple is yellow (mid-migration)", func(t *testing.T) {
		b := &darwinBackend{
			glob: fakeGlob(map[string][]string{dir: {fileMedium, fileSmall}}),
			stat: fakeStat(map[string]fileStat{
				fileMedium: {Size: 539 * 1024 * 1024},
				fileSmall:  {Size: 466 * 1024 * 1024},
			}),
		}
		res := b.probeWhisperModel(context.Background())
		if res.State != "multiple" || res.Status != healthprobes.StatusYellow {
			t.Errorf("got state=%q status=%q, want multiple/yellow", res.State, res.Status)
		}
	})

	t.Run("zero-byte is red", func(t *testing.T) {
		b := &darwinBackend{
			glob: fakeGlob(map[string][]string{dir: {fileMedium}}),
			stat: fakeStat(map[string]fileStat{fileMedium: {Size: 0}}),
		}
		res := b.probeWhisperModel(context.Background())
		if res.State != "zero_byte" || res.Status != healthprobes.StatusRed {
			t.Errorf("got state=%q status=%q, want zero_byte/red", res.State, res.Status)
		}
	})
}

func TestProbeUSBAudio(t *testing.T) {
	const cmd = "system_profiler SPAudioDataType"
	t.Run("detected", func(t *testing.T) {
		out := "Audio:\n\n    Devices:\n\n        Advanced USB Audio:\n          Input Channels: 2\n"
		b := &darwinBackend{run: fakeRunner{results: map[string]cmdResult{cmd: {stdout: out}}}.run}
		res := b.probeUSBAudio(context.Background())
		if res.Name != healthprobes.ProbeUSBAudio {
			t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbeUSBAudio)
		}
		if res.State != "detected" {
			t.Errorf("State = %q, want detected", res.State)
		}
		if res.Status != healthprobes.StatusGreen {
			t.Errorf("Status = %q, want green", res.Status)
		}
	})
	t.Run("missing", func(t *testing.T) {
		out := "Audio:\n\n    Devices:\n\n        MacBook Pro Speakers:\n"
		b := &darwinBackend{run: fakeRunner{results: map[string]cmdResult{cmd: {stdout: out}}}.run}
		res := b.probeUSBAudio(context.Background())
		if res.State != "missing" {
			t.Errorf("State = %q, want missing", res.State)
		}
		if res.Status != healthprobes.StatusRed {
			t.Errorf("Status = %q, want red", res.Status)
		}
	})
}

func TestProbeGUISession(t *testing.T) {
	cases := map[string]struct {
		consoleUser string
		wantState   string
		wantStatus  healthprobes.Status
	}{
		"expected user active":       {"uknomi", "active", healthprobes.StatusGreen},
		"login window (root owns)":   {"root", "login_window", healthprobes.StatusRed},
		"different user lingering":   {"admin", "different_user", healthprobes.StatusYellow},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := &darwinBackend{
				run: fakeRunner{results: map[string]cmdResult{
					"stat -f %Su /dev/console": {stdout: tc.consoleUser + "\n"},
				}}.run,
				expectedLoginUser: "uknomi",
			}
			res := b.probeGUISession(context.Background())
			if res.Name != healthprobes.ProbeGUISession {
				t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbeGUISession)
			}
			if res.State != tc.wantState {
				t.Errorf("State = %q, want %q", res.State, tc.wantState)
			}
			if res.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", res.Status, tc.wantStatus)
			}
			if res.Details["console_user"] != tc.consoleUser {
				t.Errorf("Details[console_user] = %v, want %q", res.Details["console_user"], tc.consoleUser)
			}
		})
	}
}

func TestProbePlateRecognizerContainer(t *testing.T) {
	const cmd = "docker ps -a --filter name=plate-recognizer-stream --format {{.Status}}"
	cases := map[string]struct {
		result     cmdResult
		wantState  string
		wantStatus healthprobes.Status
	}{
		"running":           {cmdResult{stdout: "Up 3 days\n"}, "running", healthprobes.StatusGreen},
		"stopped (exited)":  {cmdResult{stdout: "Exited (137) 2 hours ago\n"}, "stopped", healthprobes.StatusRed},
		"missing (no rows)": {cmdResult{stdout: "\n"}, "missing", healthprobes.StatusRed},
		"docker unreachable": {
			cmdResult{stderr: "Cannot connect to the Docker daemon", err: errors.New("exit status 1")},
			"docker_unreachable", healthprobes.StatusRed,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := &darwinBackend{
				run: fakeRunner{results: map[string]cmdResult{cmd: tc.result}}.run,
			}
			res := b.probePlateRecognizerContainer(context.Background())
			if res.Name != healthprobes.ProbePlateRecognizerContainer {
				t.Errorf("Name = %q, want %q", res.Name, healthprobes.ProbePlateRecognizerContainer)
			}
			if res.State != tc.wantState {
				t.Errorf("State = %q, want %q", res.State, tc.wantState)
			}
			if res.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", res.Status, tc.wantStatus)
			}
		})
	}
}

func TestProbeAutoLoginCorrupted(t *testing.T) {
	cases := map[string]fileStat{
		"world-readable mode":  {Mode: 0o644, UID: 0, GID: 0},
		"not owned by root":    {Mode: 0o600, UID: 501, GID: 20},
	}
	for name, fs := range cases {
		t.Run(name, func(t *testing.T) {
			b := &darwinBackend{
				run: fakeRunner{results: map[string]cmdResult{
					"defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser": {stdout: "uknomi\n"},
				}}.run,
				stat:              fakeStat(map[string]fileStat{"/etc/kcpassword": fs}),
				expectedLoginUser: "uknomi",
			}
			res := b.probeAutoLogin(context.Background())
			if res.State != "corrupted" {
				t.Errorf("State = %q, want %q", res.State, "corrupted")
			}
			if res.Status != healthprobes.StatusRed {
				t.Errorf("Status = %q, want red", res.Status)
			}
		})
	}
}
