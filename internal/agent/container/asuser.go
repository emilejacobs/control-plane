package container

import (
	"context"
	"fmt"
	"os/exec"
	osuser "os/user"
	"strings"
)

// AsUserArgs builds the argv that runs `name args...` inside the target user's
// GUI session: `launchctl asuser <uid> sudo -u <user> name args...`. The
// asuser wrapper is what gives the command access to the per-user Colima VM,
// which a plain `sudo -u` from a root LaunchDaemon would miss (ADR-038).
func AsUserArgs(uid, user, name string, args []string) []string {
	base := []string{"launchctl", "asuser", uid, "sudo", "-u", user, name}
	return append(base, args...)
}

// AsUserRunner is the production UserRunner: it resolves the user's uid once and
// runs each command via AsUserArgs.
type AsUserRunner struct {
	user string
	uid  string
}

// NewAsUserRunner resolves user's uid and returns a runner bound to it.
func NewAsUserRunner(user string) (*AsUserRunner, error) {
	u, err := osuser.Lookup(user)
	if err != nil {
		return nil, fmt.Errorf("lookup user %q: %w", user, err)
	}
	return &AsUserRunner{user: user, uid: u.Uid}, nil
}

func (r *AsUserRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	full := AsUserArgs(r.uid, r.user, name, args)
	return exec.CommandContext(ctx, full[0], full[1:]...).CombinedOutput()
}

// ColimaRunning reports whether the per-user Colima VM is up (`colima status`
// exits 0 when running). Used for service-status visibility — auto-login is
// load-bearing for the VM, so the CP should see when it's down (ADR-038 §3).
func (m *Manager) ColimaRunning(ctx context.Context) bool {
	_, err := m.run.Run(ctx, "colima", "status")
	return err == nil
}

// ContainerRunning reports whether the ALPR container is currently running.
func (m *Manager) ContainerRunning(ctx context.Context) bool {
	out, err := m.run.Run(ctx, "docker", "ps", "--filter", "name="+m.cfg.ContainerName, "--format", "{{.Names}}")
	if err != nil {
		return false
	}
	return strings.Contains(string(out), m.cfg.ContainerName)
}
