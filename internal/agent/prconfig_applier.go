package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/emilejacobs/control-plane/internal/agent/container"
	"github.com/emilejacobs/control-plane/internal/agent/prconfigini"
	"github.com/emilejacobs/control-plane/internal/protocol/prconfig"
)

// prConfigApplier is the production prconfigupdate.Applier (#5): it MERGES the
// CP-managed fields into the on-disk config.ini (preserving the hand-tuned and
// device-specific keys) and bounces the ALPR container through the per-user
// Colima runner (#89, ADR-038). config.ini lives in the bind-mounted stream
// dir, so writing it there makes it visible in the container — no docker cp.
type prConfigApplier struct {
	configPath string
	readFile   func(string) ([]byte, error)
	writeFile  func(string, []byte, os.FileMode) error
	restart    func(ctx context.Context) error // nil when no auto-login user (ALPR unavailable)
}

func (a *prConfigApplier) Apply(ctx context.Context, req prconfig.UpdateRequest) error {
	existing, err := a.readFile(a.configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", a.configPath, err)
	}
	merged, err := prconfigini.Merge(existing, req.Config, req.LPRCameraRtspURL)
	if err != nil {
		return fmt.Errorf("merge config.ini: %w", err)
	}
	if err := a.writeFile(a.configPath, merged, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", a.configPath, err)
	}
	if a.restart == nil {
		return fmt.Errorf("ALPR unavailable: no container runner (auto-login user not configured)")
	}
	return a.restart(ctx)
}

// newPRConfigApplier builds the production applier. The container restart is
// wired only when an auto-login user is configured — Colima runs as that user
// (#89); otherwise Apply still writes config.ini but reports the restart gap.
func newPRConfigApplier(streamDir, autoLoginUser string) *prConfigApplier {
	a := &prConfigApplier{
		configPath: filepath.Join(streamDir, "config.ini"),
		readFile:   os.ReadFile,
		writeFile:  os.WriteFile,
	}
	if autoLoginUser != "" {
		if runner, err := container.NewAsUserRunner(autoLoginUser); err == nil {
			mgr := container.New(runner, container.Config{
				StreamDir:     streamDir,
				ContainerName: "plate-recognizer-stream",
				Image:         alprImage(),
				HostPort:      8050,
			})
			a.restart = mgr.Restart
		}
	}
	return a
}
