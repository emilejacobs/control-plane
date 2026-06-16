// Package container drives the Plate Recognizer (ALPR) container under Colima
// (ADR-038). Colima runs a per-user VM as the non-root install user, but the
// agent runs as a root LaunchDaemon — so every docker/colima command is routed
// through the user's session via `launchctl asuser <uid> sudo -u <user> …`
// (the UserRunner seam). The user LaunchAgent owns the VM lifecycle; this
// module owns only the workload (start/restart/logs + the camera config).
package container

import (
	"context"
	"fmt"
	"strconv"
)

// UserRunner runs a command inside the Colima user's session and returns its
// combined output. The production implementation wraps launchctl asuser +
// sudo -u; tests fake it.
type UserRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Config identifies the ALPR container and its host mount.
type Config struct {
	StreamDir     string // host dir bind-mounted to /user-data (holds config.ini)
	ContainerName string // e.g. plate-recognizer-stream
	Image         string // e.g. platerecognizer/alpr-stream:arm
	HostPort      int    // host port mapped to the container's 8050
}

// Manager performs container lifecycle operations through a UserRunner.
type Manager struct {
	run UserRunner
	cfg Config
}

// New returns a Manager driving cfg's container via run.
func New(run UserRunner, cfg Config) *Manager {
	return &Manager{run: run, cfg: cfg}
}

// StartALPR (re)creates the ALPR container with the per-device license + token.
// The license is consumed only here — at Commission — per ADR-036 §2. Any prior
// container is removed first so the call is repeatable.
func (m *Manager) StartALPR(ctx context.Context, licenseKey, token string) error {
	// Ensure the image is present first. It is pulled lazily here, at first
	// Commission, rather than during install: Colima needs the user's GUI
	// session (auto-login), which may not exist yet when the root pkg
	// postinstall runs (ADR-038).
	if err := m.EnsureImage(ctx); err != nil {
		return err
	}

	// Best-effort removal of a stale container; ignore the error (none to remove).
	_, _ = m.run.Run(ctx, "docker", "rm", "-f", m.cfg.ContainerName)

	_, err := m.run.Run(ctx, "docker", "run", "-d",
		"--restart=unless-stopped",
		"--name", m.cfg.ContainerName,
		"-v", m.cfg.StreamDir+":/user-data",
		"-e", "LICENSE_KEY="+licenseKey,
		"-e", "TOKEN="+token,
		"-p", fmt.Sprintf("%d:8050", m.cfg.HostPort),
		m.cfg.Image,
	)
	if err != nil {
		return fmt.Errorf("docker run %s: %w", m.cfg.ContainerName, err)
	}
	return nil
}

// Restart bounces the container — used after config.ini changes so the new
// camera RTSP URLs take effect.
func (m *Manager) Restart(ctx context.Context) error {
	if _, err := m.run.Run(ctx, "docker", "restart", m.cfg.ContainerName); err != nil {
		return fmt.Errorf("docker restart %s: %w", m.cfg.ContainerName, err)
	}
	return nil
}

// Logs returns the last n lines of the container's output — the log.tail docker
// kind for the plate-recognizer container.
func (m *Manager) Logs(ctx context.Context, lines int) ([]byte, error) {
	out, err := m.run.Run(ctx, "docker", "logs", "--tail", strconv.Itoa(lines), m.cfg.ContainerName)
	if err != nil {
		return nil, fmt.Errorf("docker logs %s: %w", m.cfg.ContainerName, err)
	}
	return out, nil
}

// EnsureImage pulls the ALPR image when it isn't already present locally.
// Idempotent: `docker image inspect` succeeds when the image exists, so a
// re-run is a no-op.
func (m *Manager) EnsureImage(ctx context.Context) error {
	if _, err := m.run.Run(ctx, "docker", "image", "inspect", m.cfg.Image); err == nil {
		return nil
	}
	if _, err := m.run.Run(ctx, "docker", "pull", m.cfg.Image); err != nil {
		return fmt.Errorf("docker pull %s: %w", m.cfg.Image, err)
	}
	return nil
}
