//go:build darwin

package probes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	now               func() time.Time
	expectedLoginUser string
	logger            *slog.Logger

	// Colima runtime (ADR-038): the per-user Colima VM that runs the ALPR
	// container post-migration. The root agent reaches it via
	// `launchctl asuser <colimaUID> sudo -u <colimaUser> <dockerBin> …`.
	// Empty colimaUser/UID falls back to root Docker Desktop (pre-migration).
	colimaUser string
	colimaUID  string
	dockerBin  string
}

// NewSystemBackend returns a darwin probe backend wired to the real
// exec + filesystem. expectedLoginUser is the auto-login user the fleet
// expects (e.g. "uknomi"); pass nil logger to discard.
func NewSystemBackend(expectedLoginUser string, logger *slog.Logger) Backend {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	b := &darwinBackend{
		run:               execRun,
		stat:              statReal,
		readFile:          os.ReadFile,
		glob:              filepath.Glob,
		now:               time.Now,
		expectedLoginUser: expectedLoginUser,
		logger:            logger,
		dockerBin:         resolveDockerBin(),
	}
	// The auto-login user runs the per-user Colima VM (ADR-038). Resolve its uid
	// once so docker probes can drop into its session via launchctl asuser. If
	// the user can't be resolved, leave it empty — the probe falls back to root
	// Docker Desktop (correct for not-yet-migrated devices).
	if expectedLoginUser != "" {
		if u, err := osuser.Lookup(expectedLoginUser); err == nil {
			b.colimaUser = expectedLoginUser
			b.colimaUID = u.Uid
		}
	}
	return b
}

// resolveDockerBin finds the docker CLI by absolute path. Under
// `launchctl asuser … sudo -u …`, sudo's secure_path won't include Homebrew, so
// a bare "docker" wouldn't resolve — and Homebrew's arm64 prefix isn't on the
// LaunchDaemon's PATH either. Prefer the brew (Colima) docker, then Docker
// Desktop's, then a bare name as a last resort.
func resolveDockerBin() string {
	for _, p := range []string{"/opt/homebrew/bin/docker", "/usr/local/bin/docker"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "docker"
}

// colimaDockerArgv wraps a docker invocation to run inside the auto-login user's
// Colima session (ADR-038 §2): launchctl asuser <uid> sudo -u <user> <bin> args…
func colimaDockerArgv(uid, user, dockerBin string, args []string) []string {
	base := []string{"launchctl", "asuser", uid, "sudo", "-u", user, dockerBin}
	return append(base, args...)
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
		b.probeBootSanity(ctx),
	}
}

const bootFlappingThreshold = 5

var bootSecPattern = regexp.MustCompile(`sec\s*=\s*(\d+)`)

// probeBootSanity reports uptime and how often the device rebooted in
// the last 7 days. A device cycling every few hours is sick even if it
// is currently up — boots_last_7d > 5 is red.
func (b *darwinBackend) probeBootSanity(ctx context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbeBootSanity, Details: map[string]any{}}
	now := b.now()

	if stdout, _, err := b.run(ctx, "sysctl", "-n", "kern.boottime"); err == nil {
		if m := bootSecPattern.FindSubmatch(stdout); m != nil {
			if sec, perr := strconv.ParseInt(string(m[1]), 10, 64); perr == nil {
				res.Details["uptime_s"] = now.Unix() - sec
			}
		}
	}

	stdout, _, _ := b.run(ctx, "last", "reboot")
	boots := parseRebootCount(string(stdout), now)
	res.Details["boots_last_7d"] = boots

	if boots > bootFlappingThreshold {
		res.State = "flapping"
		res.Status = healthprobes.StatusRed
	} else {
		res.State = "stable"
		res.Status = healthprobes.StatusGreen
	}
	return res
}

// parseRebootCount counts `last reboot` entries within 7 days of now.
// `last` omits the year, so we assume now's year and roll back one year
// for dates that would otherwise be in the future.
func parseRebootCount(out string, now time.Time) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// reboot  ~  <Weekday> <Month> <Day> <HH:MM>
		if len(fields) < 6 || fields[0] != "reboot" {
			continue
		}
		stamp := fields[len(fields)-3] + " " + fields[len(fields)-2] + " " + fields[len(fields)-1]
		t, err := time.ParseInLocation("Jan 2 15:04", stamp, now.Location())
		if err != nil {
			continue
		}
		t = t.AddDate(now.Year(), 0, 0)
		if t.After(now) {
			t = t.AddDate(-1, 0, 0)
		}
		if d := now.Sub(t); d >= 0 && d <= 7*24*time.Hour {
			count++
		}
	}
	return count
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

// usbAudioTransport is the coreaudio transport value system_profiler
// reports for USB-attached audio devices (built-in audio reports
// coreaudio_device_type_builtin). We detect by transport rather than a
// hard-coded product name: the capture dongle's _name varies by vendor
// (e.g. "USB Audio Device" / C-Media), so matching a fixed name made the
// probe report "missing" even with a working dongle present.
const usbAudioTransport = "coreaudio_device_type_usb"

// probeUSBAudio reports whether a USB audio capture device is enumerated
// by the OS (the cause behind silent no-recording symptoms; see #10 for
// the symptom-side check). Reports the detected device name + input
// channel count in details so operators can confirm it's the right one.
func (b *darwinBackend) probeUSBAudio(ctx context.Context) healthprobes.Result {
	res := healthprobes.Result{Name: healthprobes.ProbeUSBAudio, Details: map[string]any{}}

	stdout, _, err := b.run(ctx, "system_profiler", "SPAudioDataType", "-json")
	if err != nil {
		res.State = "missing"
		res.Status = healthprobes.StatusRed
		return res
	}
	if name, channels, ok := firstUSBAudioDevice(stdout); ok {
		res.Details["device"] = name
		res.Details["input_channels"] = channels
		res.State = "detected"
		res.Status = healthprobes.StatusGreen
		return res
	}
	res.State = "missing"
	res.Status = healthprobes.StatusRed
	return res
}

// firstUSBAudioDevice parses `system_profiler SPAudioDataType -json` and
// returns the first audio device whose transport is USB.
func firstUSBAudioDevice(jsonOut []byte) (name string, inputChannels int, ok bool) {
	var doc struct {
		Audio []struct {
			Items []struct {
				Name      string `json:"_name"`
				Transport string `json:"coreaudio_device_transport"`
				Input     int    `json:"coreaudio_device_input"`
			} `json:"_items"`
		} `json:"SPAudioDataType"`
	}
	if err := json.Unmarshal(jsonOut, &doc); err != nil {
		return "", 0, false
	}
	for _, section := range doc.Audio {
		for _, d := range section.Items {
			if d.Transport == usbAudioTransport {
				return d.Name, d.Input, true
			}
		}
	}
	return "", 0, false
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

	status, runtime, reachable := b.containerStatus(ctx)
	if !reachable {
		res.State = "docker_unreachable"
		res.Status = healthprobes.StatusRed
		return res
	}

	res.Details["docker_status"] = status
	if runtime != "" {
		res.Details["runtime"] = runtime
	}
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

// containerStatus reports the ALPR container's `docker ps` status string, the
// runtime it was found in, and whether any runtime was reachable. It checks the
// per-user Colima daemon first (post-migration, ADR-038) and falls back to root
// Docker Desktop (pre-migration) — so it's correct across the mixed fleet during
// the Docker→Colima rollout.
func (b *darwinBackend) containerStatus(ctx context.Context) (status, runtime string, reachable bool) {
	psArgs := []string{"ps", "-a", "--filter", "name=" + plateRecognizerContainerName, "--format", "{{.Status}}"}

	// 1. Colima (post-migration): drop into the auto-login user's session and
	//    target the colima context explicitly (don't depend on its default ctx).
	if b.colimaUser != "" && b.colimaUID != "" && b.dockerBin != "" {
		argv := colimaDockerArgv(b.colimaUID, b.colimaUser, b.dockerBin, append([]string{"--context", "colima"}, psArgs...))
		if out, _, err := b.run(ctx, argv[0], argv[1:]...); err == nil {
			reachable = true
			if s := strings.TrimSpace(string(out)); s != "" {
				return s, "colima", true
			}
		}
	}

	// 2. Docker Desktop (pre-migration): plain root docker against its socket.
	if out, _, err := b.run(ctx, "docker", psArgs...); err == nil {
		reachable = true
		if s := strings.TrimSpace(string(out)); s != "" {
			return s, "docker", true
		}
	}

	return "", "", reachable
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
