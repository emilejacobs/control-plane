package container_test

import (
	"context"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/agent/container"
)

// AsUserArgs wraps a command so it runs in the target user's GUI session
// (needed for the per-user Colima VM): launchctl asuser <uid> sudo -u <user> …
func TestAsUserArgs(t *testing.T) {
	got := container.AsUserArgs("501", "uknomi", "docker", []string{"ps", "-a"})
	want := []string{"launchctl", "asuser", "501", "sudo", "-u", "uknomi", "docker", "ps", "-a"}
	if len(got) != len(want) {
		t.Fatalf("len: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d: got %q want %q", i, got[i], want[i])
		}
	}
}

// ColimaRunning reflects `colima status` success/failure.
func TestColimaRunning(t *testing.T) {
	up := container.New(&fakeRunner{}, sampleConfig())
	if !up.ColimaRunning(context.Background()) {
		t.Error("expected running when colima status succeeds")
	}
	down := container.New(&fakeRunner{err: errors.New("colima not running")}, sampleConfig())
	if down.ColimaRunning(context.Background()) {
		t.Error("expected not-running when colima status errors")
	}
}

// ContainerRunning reflects whether docker ps lists the container by name.
func TestContainerRunning(t *testing.T) {
	fr := &fakeRunner{output: map[string]string{
		"docker ps --filter name=plate-recognizer-stream --format {{.Names}}": "plate-recognizer-stream\n",
	}}
	if !container.New(fr, sampleConfig()).ContainerRunning(context.Background()) {
		t.Error("expected running when docker ps lists the container")
	}
	if container.New(&fakeRunner{}, sampleConfig()).ContainerRunning(context.Background()) {
		t.Error("expected not-running when docker ps lists nothing")
	}
}
