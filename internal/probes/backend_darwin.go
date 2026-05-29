//go:build darwin

package probes

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
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
}

// statFunc abstracts os.Stat + Stat_t so unit tests can stage canned
// file modes/owners without touching the real filesystem.
type statFunc func(path string) (fileStat, error)

const loginWindowPlist = "/Library/Preferences/com.apple.loginwindow"

const kcpasswordPath = "/etc/kcpassword"

type darwinBackend struct {
	run               runner
	stat              statFunc
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
	fs := fileStat{Mode: info.Mode().Perm()}
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
	}
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
