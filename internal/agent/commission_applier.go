package agent

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/emilejacobs/control-plane/internal/agent/container"
)

// tailscaleUpFunc runs `tailscale <args...>`; injectable so the applier is
// testable without a real tailscaled.
type tailscaleUpFunc func(ctx context.Context, args ...string) error

// commissionApplier is the production commission.Applier (#91): it joins the
// tailnet with the minted key and starts the ALPR container through the
// per-user Colima runner (#89, ADR-038).
type commissionApplier struct {
	tsUp      tailscaleUpFunc
	alprStart func(ctx context.Context, license, token string) error
}

func (a *commissionApplier) JoinTailnet(ctx context.Context, authKey string) error {
	if err := a.tsUp(ctx, "up", "--authkey="+authKey); err != nil {
		return fmt.Errorf("tailscale up: %w", err)
	}
	return nil
}

func (a *commissionApplier) StartALPR(ctx context.Context, license, token string) error {
	if a.alprStart == nil {
		return fmt.Errorf("ALPR unavailable: no container runner (auto-login user not configured)")
	}
	return a.alprStart(ctx, license, token)
}

func execTailscaleUp(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "tailscale", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

func alprImage() string {
	if runtime.GOARCH == "arm64" {
		return "platerecognizer/alpr-stream:arm"
	}
	return "platerecognizer/alpr-stream:latest"
}

// newCommissionApplier builds the production applier. The ALPR start is wired
// only when an auto-login user is configured — Colima runs as that user (#89);
// otherwise StartALPR returns a clear error and tailnet-only commission still
// works.
func newCommissionApplier(autoLoginUser string) *commissionApplier {
	a := &commissionApplier{tsUp: execTailscaleUp}
	if autoLoginUser != "" {
		if runner, err := container.NewAsUserRunner(autoLoginUser); err == nil {
			mgr := container.New(runner, container.Config{
				StreamDir:     "/usr/local/etc/plate-recognizer/stream",
				ContainerName: "plate-recognizer-stream",
				Image:         alprImage(),
				HostPort:      8050,
			})
			a.alprStart = mgr.StartALPR
		}
	}
	return a
}
