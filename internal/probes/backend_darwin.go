//go:build darwin

package probes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// runner abstracts the exec shell-out so unit tests can stage canned
// stdout/stderr without spawning real processes (mirrors internal/service).
type runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

// fileStat is the subset of file metadata the probes care about.
type fileStat struct {
	Mode os.FileMode
	UID  uint32
	GID  uint32
	Size int64
}

// statFunc abstracts os.Stat + Stat_t so unit tests can stage canned
// file modes/owners without touching the real filesystem.
type statFunc func(path string) (fileStat, error)

// fileReadFunc abstracts os.ReadFile so unit tests can stage canned
// file contents (used for config-integrity hashing).
type fileReadFunc func(path string) ([]byte, error)

// globFunc abstracts filepath.Glob so unit tests can stage canned
// directory listings.
type globFunc func(pattern string) ([]string, error)

const loginWindowPlist = "/Library/Preferences/com.apple.loginwindow"

const kcpasswordPath = "/etc/kcpassword"

type darwinBackend struct {
	run               runner
	stat              statFunc
	readFile          fileReadFunc
	glob              globFunc
	expectedLoginUser string
	logger            *slog.Logger
}

// NewSystemBackend returns a darwin probe backend wired to the real
// exec + filesystem. expectedLoginUser is the auto-login user the fleet
// expects (e.g. "uknomi"); pass nil logger to discard.
func NewSystemBackend(expectedLoginUser string, logger *slog.Logger) Backend {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &darwinBackend{
		run:               execRun,
		stat:              statReal,
		readFile:          os.ReadFile,
		glob:              filepath.Glob,
		expectedLoginUser: expectedLoginUser,
		logger:            logger,
	}
}

func execRun(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func statReal(path string) (fileStat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStat{}, err
	}
	fs := fileStat{Mode: info.Mode().Perm(), Size: info.Size()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		fs.UID = st.Uid
		fs.GID = st.Gid
	}
	return fs, nil
}

// Collect runs every probe. Slice 1 grows this list one probe at a time.
func (b *darwinBackend) Collect(ctx context.Context) []healthprobes.Result {
	return []healthprobes.Result{
		b.probeAutoLogin(ctx),
		b.probeGUISession(ctx),
		b.probePlateRecognizerContainer(ctx),
		b.probePlateRecognizerConfig(ctx),
		b.probeUSBAudio(ctx),
		b.probeWhisperModel(ctx),
	}
}

const whisperModelGlob = "/usr/local/etc/uknomi/whisper-models/*.bin"

// quantPattern matches whisper.cpp quantization suffixes (q5_0, q8_0, f16, f32, ...).
var quantPattern = regexp.MustCompile(`^(q\d_\d|f16|f32)$`)

// parseWhisperFilename extracts the model variant and quantization from
// a whisper.cpp model filename like "ggml-medium.en-q5_0.bin". The
// variant itself may contain hyphens ("large-v3"), so quantization is
// only the trailing segment when it matches a known quant suffix.
func parseWhisperFilename(name string) (variant, quantization string) {
	base := strings.TrimSuffix(filepath.Base(name), ".bin")
	base = strings.TrimPrefix(base, "ggml-")
	segs := strings.Split(base, "-")
	if len(segs) > 1 && quantPattern.MatchString(segs[len(segs)-1]) {
		quantization = segs[len(segs)-1]
		segs = segs[:len(segs)-1]
	}
	return strings.Join(segs, "-"), quantization
}

// probeWhisperModel reports which whisper model(s) are installed. The
// model is curl'd from HuggingFace during install with no verification,
// so this catches silent download failure and gives fleet-wide
// visibility on which variant is deployed where (for model migrations).
func (b *darwinBackend) probeWhisperModel(_ context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbeWhisperModel, Details: map[string]any{}}

	matches, _ := b.glob(whisperModelGlob)
	switch len(matches) {
	case 0:
		res.State = "missing"
		res.Status = healthprobes.StatusRed
		return res
	case 1:
		path := matches[0]
		variant, quant := parseWhisperFilename(path)
		var size int64
		if fs, err := b.stat(path); err == nil {
			size = fs.Size
		}
		if size == 0 {
			res.State = "zero_byte"
			res.Status = healthprobes.StatusRed
			res.Details["file"] = filepath.Base(path)
			return res
		}
		res.State = "present"
		res.Status = healthprobes.StatusGreen
		res.Details["variant"] = variant
		res.Details["quantization"] = quant
		res.Details["size_mb"] = int(size / (1024 * 1024))
		res.Details["file"] = filepath.Base(path)
		return res
	default:
		models := make([]map[string]any, 0, len(matches))
		for _, path := range matches {
			variant, quant := parseWhisperFilename(path)
			var size int64
			if fs, err := b.stat(path); err == nil {
				size = fs.Size
			}
			models = append(models, map[string]any{
				"file":         filepath.Base(path),
				"variant":      variant,
				"quantization": quant,
				"size_mb":      int(size / (1024 * 1024)),
			})
		}
		res.State = "multiple"
		res.Status = healthprobes.StatusYellow
		res.Details["models"] = models
		return res
	}
}

// usbAudioDeviceName is the USB audio dongle the fleet records with.
// macOS intermittently fails to enumerate it across reboots — the
// recurring failure this probe catches.
const usbAudioDeviceName = "Advanced USB Audio"

// probeUSBAudio reports whether the USB audio capture device is
// enumerated by the OS (the cause behind silent no-recording symptoms;
// see #10 for the symptom-side check).
func (b *darwinBackend) probeUSBAudio(ctx context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbeUSBAudio, Details: map[string]any{}}

	stdout, _, err := b.run(ctx, "system_profiler", "SPAudioDataType")
	if err == nil && strings.Contains(string(stdout), usbAudioDeviceName) {
		res.State = "detected"
		res.Status = healthprobes.StatusGreen
		return res
	}
	res.State = "missing"
	res.Status = healthprobes.StatusRed
	return res
}

const plateRecognizerConfigPath = "/usr/local/etc/plate-recognizer/stream/config.ini"

// probePlateRecognizerConfig hashes the Plate Recognizer config.ini so
// operators can spot accidental deletion or drift from intended config.
// The PR service has no usable web UI — config.ini on disk is the source
// of truth (see memory plate_recognizer_no_web_ui).
func (b *darwinBackend) probePlateRecognizerConfig(_ context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbePlateRecognizerConfig, Details: map[string]any{}}

	data, err := b.readFile(plateRecognizerConfigPath)
	if err != nil {
		res.State = "missing"
		res.Status = healthprobes.StatusRed
		return res
	}

	sum := sha256.Sum256(data)
	res.Details["sha256"] = hex.EncodeToString(sum[:])
	res.Details["size_bytes"] = len(data)
	res.State = "present"
	res.Status = healthprobes.StatusGreen
	return res
}

const plateRecognizerContainerName = "plate-recognizer-stream"

// probePlateRecognizerContainer reports the Plate Recognizer container's
// state via `docker ps -a`. NOTE (ADR-034 / #19 brief): the agent runs as
// root but Docker Desktop's daemon is per-user, so when no one is logged
// in the socket is unreachable — docker_unreachable is then the correct
// signal (and auto-login has usually failed too).
func (b *darwinBackend) probePlateRecognizerContainer(ctx context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbePlateRecognizerContainer, Details: map[string]any{}}

	stdout, _, err := b.run(ctx, "docker", "ps", "-a",
		"--filter", "name="+plateRecognizerContainerName, "--format", "{{.Status}}")
	if err != nil {
		res.State = "docker_unreachable"
		res.Status = healthprobes.StatusRed
		return res
	}

	status := strings.TrimSpace(string(stdout))
	res.Details["docker_status"] = status
	switch {
	case status == "":
		res.State = "missing"
		res.Status = healthprobes.StatusRed
	case strings.HasPrefix(status, "Up"):
		res.State = "running"
		res.Status = healthprobes.StatusGreen
	default:
		res.State = "stopped"
		res.Status = healthprobes.StatusRed
	}
	return res
}

// probeGUISession reports who owns /dev/console — the macOS convention
// for "the user logged into the GUI". When auto-login fails the Mac
// sits at the login window with /dev/console owned by root; this probe
// is what distinguishes "auto-login attempted but failed" from healthy.
func (b *darwinBackend) probeGUISession(ctx context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbeGUISession, Details: map[string]any{}}

	stdout, _, err := b.run(ctx, "stat", "-f", "%Su", "/dev/console")
	user := strings.TrimSpace(string(stdout))
	res.Details["console_user"] = user

	switch {
	case err == nil && user == b.expectedLoginUser:
		res.State = "active"
		res.Status = healthprobes.StatusGreen
	case user == "root" || user == "":
		res.State = "login_window"
		res.Status = healthprobes.StatusRed
	default:
		res.State = "different_user"
		res.Status = healthprobes.StatusYellow
	}
	return res
}

// probeAutoLogin reports whether passwordless auto-login is wired:
// the loginwindow autoLoginUser matches the expected user AND
// /etc/kcpassword exists with mode 0600 owned by root:wheel. This is
// the 9-day dead-zone failure mode from the 2026-05-27 diagnostic.
func (b *darwinBackend) probeAutoLogin(ctx context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbeAutoLogin, Details: map[string]any{}}

	stdout, _, err := b.run(ctx, "defaults", "read", loginWindowPlist, "autoLoginUser")
	user := strings.TrimSpace(string(stdout))
	if err != nil || user == "" || user != b.expectedLoginUser {
		res.Details["configured_user"] = user
		res.Details["expected_user"] = b.expectedLoginUser
		res.State = "missing"
		res.Status = healthprobes.StatusRed
		return res
	}
	res.Details["configured_user"] = user

	fs, err := b.stat(kcpasswordPath)
	if err != nil {
		res.State = "missing"
		res.Status = healthprobes.StatusRed
		return res
	}
	if fs.Mode.Perm() != 0o600 || fs.UID != 0 || fs.GID != 0 {
		res.Details["mode"] = fs.Mode.Perm().String()
		res.State = "corrupted"
		res.Status = healthprobes.StatusRed
		return res
	}

	res.State = "configured"
	res.Status = healthprobes.StatusGreen
	return res
}
