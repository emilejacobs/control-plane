package install

import "context"

// EnrollStep performs Enrollment as an install step. Idempotent by inspection:
// done once the agent-config exists (enroll writes it). Apply delegates to the
// injected enroll func — a closure over the enroll package — so the step stays
// testable without real HTTP/file IO, and enrollment runs after the binaries
// are in place (ADR-036 ordering).
type EnrollStep struct {
	Sys        System
	ConfigPath string
	Enroll     func(ctx context.Context) error
}

func (s *EnrollStep) Name() string { return "enroll" }

func (s *EnrollStep) IsDone(_ context.Context) (bool, error) {
	return s.Sys.Exists(s.ConfigPath)
}

func (s *EnrollStep) Apply(ctx context.Context) error {
	return s.Enroll(ctx)
}

// Plan is the concrete input for a Provision: where the packaged binaries are,
// where they land, the enrollment closure, and the LaunchDaemon definition.
type Plan struct {
	AgentSrc      string
	AgentDst      string
	SupervisorSrc string
	SupervisorDst string

	ConfigPath string
	Enroll     func(ctx context.Context) error

	Label     string
	PlistPath string
	Plist     []byte
}

// PlanSteps returns the core Provision steps in order: install the binaries,
// enroll (writing the config), then write + load the LaunchDaemon last so the
// agent only starts once its binaries and config are in place. Callers prepend
// SoftwareSteps (#88) and the hostconfig steps (#87) ahead of these.
func PlanSteps(sys System, p Plan) []Step {
	return []Step{
		&InstallBinariesStep{
			Sys:           sys,
			AgentSrc:      p.AgentSrc,
			AgentDst:      p.AgentDst,
			SupervisorSrc: p.SupervisorSrc,
			SupervisorDst: p.SupervisorDst,
		},
		&EnrollStep{
			Sys:        sys,
			ConfigPath: p.ConfigPath,
			Enroll:     p.Enroll,
		},
		&LaunchDaemonStep{
			Sys:       sys,
			Label:     p.Label,
			PlistPath: p.PlistPath,
			Plist:     p.Plist,
		},
	}
}

// BuildRunner runs just the core Provision steps (PlanSteps). The install
// subcommand composes SoftwareSteps + PlanSteps into one runner.
func BuildRunner(sys System, p Plan) *Runner {
	return NewRunner(PlanSteps(sys, p)...)
}
