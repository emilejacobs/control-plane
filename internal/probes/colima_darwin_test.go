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
