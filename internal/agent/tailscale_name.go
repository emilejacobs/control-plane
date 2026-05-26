package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// TailscaleStatusRunner is the seam for shelling out to
// `tailscale status --json`. Production wires SystemTailscaleStatusRunner
// (which invokes exec.CommandContext on PATH; AugmentSubprocessPath
// ensures /opt/homebrew/bin is reachable on Apple Silicon Macs).
// Tests pass a fake runner with canned bytes / error.
type TailscaleStatusRunner interface {
	Status(ctx context.Context) ([]byte, error)
}

// SystemTailscaleStatusRunner is the production runner — it shells
// out to `tailscale status --json` and returns the raw stdout.
type SystemTailscaleStatusRunner struct{}

func (SystemTailscaleStatusRunner) Status(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	return cmd.Output()
}

// tailscaleStatus is the minimal subset of `tailscale status --json`
// we care about — just Self.DNSName. Everything else gets dropped
// on the floor by encoding/json.
type tailscaleStatus struct {
	Self struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
}

// ResolveTailscaleName returns the device's Tailscale MagicDNS name
// (Self.DNSName with the trailing "." stripped), or "" when the
// binary is missing, the call failed, the output didn't parse, or
// the device isn't on a tailnet.
//
// The function NEVER returns a non-nil error: a heartbeat must not
// fail because tailscale isn't installed (dev box) or because
// tailscaled is logged out (pre-enrollment). Failures degrade to
// the empty-string null and the heartbeat publishes lan_ip without
// a tailscale_name.
func ResolveTailscaleName(ctx context.Context, runner TailscaleStatusRunner) (string, error) {
	if runner == nil {
		runner = SystemTailscaleStatusRunner{}
	}
	out, err := runner.Status(ctx)
	if err != nil {
		return "", nil
	}
	var s tailscaleStatus
	if err := json.Unmarshal(out, &s); err != nil {
		return "", nil
	}
	name := strings.TrimSuffix(s.Self.DNSName, ".")
	return name, nil
}
