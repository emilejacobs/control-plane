//go:build darwin

package probes

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// recordingRunner records every command it's asked to run (name + space-joined
// args) and returns staged results, so tests can assert which colima admin
// commands EnsureColima issued.
type recordingRunner struct {
	results map[string]cmdResult
	calls   []string
}

func (r *recordingRunner) run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	key := name
	for _, a := range args {
		key += " " + a
	}
	r.calls = append(r.calls, key)
	if res, ok := r.results[key]; ok {
		return []byte(res.stdout), []byte(res.stderr), res.err
	}
	return nil, nil, os.ErrNotExist
}

func (r *recordingRunner) called(substr string) bool {
	for _, c := range r.calls {
		if c == substr {
			return true
		}
	}
	return false
}

func (r *recordingRunner) countCalls(key string) int {
	n := 0
	for _, c := range r.calls {
		if c == key {
			n++
		}
	}
	return n
}

// When colima reports not-running, EnsureColima starts it via the asuser path
// (no extra args — resumes the saved profile).
func TestEnsureColima_StartsWhenStopped(t *testing.T) {
	statusKey := "launchctl asuser 442 sudo -u uknomi /opt/homebrew/bin/colima status"
	rr := &recordingRunner{results: map[string]cmdResult{
		statusKey: {err: errors.New("colima is not running")},
	}}
	b := &darwinBackend{run: rr.run, colimaUser: "uknomi", colimaUID: "442", colimaBin: "/opt/homebrew/bin/colima", logger: discardLogger()}

	b.EnsureColima(context.Background())

	if !rr.called("launchctl asuser 442 sudo -u uknomi /opt/homebrew/bin/colima start") {
		t.Errorf("expected a colima start; calls were:\n%v", rr.calls)
	}
}

// When colima reports running (status exits 0), EnsureColima does NOT start it.
func TestEnsureColima_NoopWhenRunning(t *testing.T) {
	statusKey := "launchctl asuser 442 sudo -u uknomi /opt/homebrew/bin/colima status"
	rr := &recordingRunner{results: map[string]cmdResult{
		statusKey: {stdout: "colima is running", err: nil},
	}}
	b := &darwinBackend{run: rr.run, colimaUser: "uknomi", colimaUID: "442", colimaBin: "/opt/homebrew/bin/colima", logger: discardLogger()}

	b.EnsureColima(context.Background())

	if rr.called("launchctl asuser 442 sudo -u uknomi /opt/homebrew/bin/colima start") {
		t.Errorf("must not start a running colima; calls were:\n%v", rr.calls)
	}
}

// On an un-migrated device (no colima user resolved — still Docker Desktop),
// EnsureColima does nothing at all: it must never run colima against a box that
// isn't on Colima.
func TestEnsureColima_NoopWhenUnmigrated(t *testing.T) {
	rr := &recordingRunner{results: map[string]cmdResult{}}
	b := &darwinBackend{run: rr.run, colimaUser: "", colimaUID: "", colimaBin: "/opt/homebrew/bin/colima", logger: discardLogger()}

	b.EnsureColima(context.Background())

	if len(rr.calls) != 0 {
		t.Errorf("expected no commands on an un-migrated device; got:\n%v", rr.calls)
	}
}

const (
	testStatusKey = "launchctl asuser 442 sudo -u uknomi /opt/homebrew/bin/colima status"
	testStartKey  = "launchctl asuser 442 sudo -u uknomi /opt/homebrew/bin/colima start"
	testStopKey   = "launchctl asuser 442 sudo -u uknomi /opt/homebrew/bin/colima stop -f"
)

// staleLockBackend wires a recordingRunner + a fake glob (staged disk locks) +
// a recording remove, so the stale-lock recovery path can be exercised without
// touching real processes or the filesystem.
func staleLockBackend(rr *recordingRunner, locks []string, removed *[]string) *darwinBackend {
	return &darwinBackend{
		run:        rr.run,
		colimaUser: "uknomi",
		colimaUID:  "442",
		colimaBin:  "/opt/homebrew/bin/colima",
		colimaHome: "/Users/uknomi",
		glob:       func(string) ([]string, error) { return locks, nil },
		remove:     func(p string) error { *removed = append(*removed, p); return nil },
		logger:     discardLogger(),
	}
}

// When `colima start` fails because a crashed VM left the data disk locked,
// EnsureColima reaps the leaked helper processes, clears the stale in_use_by
// lock, force-stops the instance, and retries the start once.
func TestEnsureColima_RecoversStaleDiskLock(t *testing.T) {
	lock := "/Users/uknomi/.colima/_lima/_disks/colima/in_use_by"
	rr := &recordingRunner{results: map[string]cmdResult{
		testStatusKey: {err: errors.New("colima is not running")},
		testStartKey:  {stderr: `level=fatal msg="failed to run attach disk \"colima\", in use by instance \"colima\""`, err: errors.New("exit status 1")},
	}}
	var removed []string
	b := staleLockBackend(rr, []string{lock}, &removed)

	b.EnsureColima(context.Background())

	if !rr.called("pkill -f limactl usernet") {
		t.Errorf("expected leaked usernet daemons to be reaped; calls:\n%v", rr.calls)
	}
	if !rr.called("pkill -f colima daemon start") {
		t.Errorf("expected leaked colima daemon starters to be reaped; calls:\n%v", rr.calls)
	}
	if len(removed) != 1 || removed[0] != lock {
		t.Errorf("expected stale lock %q removed, got %v", lock, removed)
	}
	if !rr.called(testStopKey) {
		t.Errorf("expected a force-stop before retry; calls:\n%v", rr.calls)
	}
	if got := rr.countCalls(testStartKey); got != 2 {
		t.Errorf("expected start attempted twice (initial + post-recovery retry), got %d", got)
	}
}

// A start failure that is NOT a stale disk lock (and leaves no lock file behind)
// must not trigger the destructive recovery — no process reaping, no lock
// removal, and no blind retry.
func TestEnsureColima_NoRecoveryOnGenericFailure(t *testing.T) {
	rr := &recordingRunner{results: map[string]cmdResult{
		testStatusKey: {err: errors.New("colima is not running")},
		testStartKey:  {stderr: "some other transient error", err: errors.New("exit status 1")},
	}}
	var removed []string
	b := staleLockBackend(rr, nil, &removed) // glob returns no locks

	b.EnsureColima(context.Background())

	if rr.called("pkill -f limactl usernet") {
		t.Errorf("must not reap processes on a non-stale-lock failure; calls:\n%v", rr.calls)
	}
	if len(removed) != 0 {
		t.Errorf("must not remove anything on a non-stale-lock failure; removed %v", removed)
	}
	if got := rr.countCalls(testStartKey); got != 1 {
		t.Errorf("expected exactly one start attempt (no retry), got %d", got)
	}
}
