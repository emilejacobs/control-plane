package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/emilejacobs/control-plane/internal/agent/enroll"
	"github.com/emilejacobs/control-plane/internal/agent/install"
)

// runInstall is the `uknomi-agent install` one-shot Provision (ADR-037): it
// lays down the agent binary + supervisor, enrolls, and loads the LaunchDaemon
// — idempotent by inspection, so a re-run resumes a partial install. The pkg's
// postinstall invokes it. #87 (hostconfig), #88 (software) and #89 (Colima)
// add their steps ahead of enrollment; this slice ships the supervisor +
// enroll skeleton.
func runInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	cpURL := fs.String("cp-url", "https://api.control.uknomi.com", "CP base URL")
	brokerURL := fs.String("broker-url", "", "MQTT broker URL, e.g. tls://<ats-endpoint>:8883 (required)")
	runtimeDir := fs.String("runtime-dir", "/var/uknomi", "agent runtime dir (cert/key/ca/config + agent-update)")
	bootstrapKeyFile := fs.String("bootstrap-key-file", "", "path to the bootstrap key file (or set CP_BOOTSTRAP_KEY)")
	caFile := fs.String("ca-file", "", "path to the AWS IoT root CA PEM (required)")
	agentSrc := fs.String("agent-src", "", "packaged agent binary (default: this executable)")
	supervisorSrc := fs.String("supervisor-src", "", "packaged supervisor (default: alongside the agent binary)")
	edgeUISrc := fs.String("edge-ui-src", "", "packaged edge-ui binary (default: alongside the agent binary)")
	brewUser := fs.String("brew-user", "uknomi", "non-root user Homebrew + Colima run as")
	brewPath := fs.String("brew-path", "/opt/homebrew/bin/brew", "brew binary path used for formula installs")
	whisperURL := fs.String("whisper-url", defaultWhisperURL, "Whisper model download URL")
	_ = fs.Parse(args)

	bootstrapKey := os.Getenv("CP_BOOTSTRAP_KEY")
	if bootstrapKey == "" && *bootstrapKeyFile != "" {
		raw, err := os.ReadFile(*bootstrapKeyFile)
		if err != nil {
			fatalCLI("read bootstrap key file: %v", err)
		}
		bootstrapKey = strings.TrimSpace(string(raw))
	}
	if bootstrapKey == "" {
		fatalCLI("bootstrap key required (--bootstrap-key-file or CP_BOOTSTRAP_KEY)")
	}
	if *brokerURL == "" {
		fatalCLI("--broker-url is required")
	}
	if *caFile == "" {
		fatalCLI("--ca-file is required")
	}
	caPEM, err := os.ReadFile(*caFile)
	if err != nil {
		fatalCLI("read CA file: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		fatalCLI("resolve executable: %v", err)
	}
	resolvedAgentSrc := *agentSrc
	if resolvedAgentSrc == "" {
		resolvedAgentSrc = exe
	}
	resolvedSupervisorSrc := *supervisorSrc
	if resolvedSupervisorSrc == "" {
		resolvedSupervisorSrc = filepath.Join(filepath.Dir(exe), "uknomi-agent-supervisor")
	}
	resolvedEdgeUISrc := *edgeUISrc
	if resolvedEdgeUISrc == "" {
		resolvedEdgeUISrc = filepath.Join(filepath.Dir(exe), "uknomi-edge-ui")
	}

	// Enrollment writes into the runtime dir; ensure it exists first.
	if err := os.MkdirAll(*runtimeDir, 0o755); err != nil {
		fatalCLI("create runtime dir: %v", err)
	}

	const supervisorDst = "/usr/local/bin/uknomi-agent-supervisor"
	agentDir := filepath.Join(*runtimeDir, "agent-update")
	configPath := filepath.Join(*runtimeDir, "agent-config.json")

	plan := install.Plan{
		AgentSrc:      resolvedAgentSrc,
		AgentDst:      filepath.Join(agentDir, "current"),
		SupervisorSrc: resolvedSupervisorSrc,
		SupervisorDst: supervisorDst,
		ConfigPath:    configPath,
		Enroll: func(ctx context.Context) error {
			_, err := enroll.Enroll(ctx, enroll.Params{
				CPBaseURL:    *cpURL,
				BootstrapKey: bootstrapKey,
				Hardware:     gatherHardware(),
				CACertPEM:    caPEM,
				RuntimeDir:   *runtimeDir,
				BrokerURL:    *brokerURL,
				Defaults:     defaultAgentConfig(),
			})
			return err
		},
		Label:     "com.uknomi.agent",
		PlistPath: "/Library/LaunchDaemons/com.uknomi.agent.plist",
		Plist: install.AgentLaunchDaemonPlist(install.AgentDaemonConfig{
			Label:          "com.uknomi.agent",
			SupervisorPath: supervisorDst,
			AgentDir:       agentDir,
			ConfigPath:     configPath,
			StdoutPath:     "/var/log/uknomi-agent.log",
			StderrPath:     "/var/log/uknomi-agent-error.log",
		}),
	}

	sys := install.NewOSSystem()
	software := install.SoftwareSteps(sys, install.SoftwareConfig{
		BrewUser:   *brewUser,
		BrewPath:   *brewPath,
		Formulae:   []string{"ffmpeg", "tailscale", "nmap", "whisper-cpp"},
		EdgeUISrc:  resolvedEdgeUISrc,
		EdgeUIDst:  "/usr/local/bin/uknomi-edge-ui",
		WhisperURL: *whisperURL,
		WhisperDst: "/usr/local/etc/uknomi/whisper-models/ggml-medium.en-q5_0.bin",
	})

	// Uniform software first, then the agent core (binaries → enroll → daemon).
	steps := append(software, install.PlanSteps(sys, plan)...)
	runner := install.NewRunner(steps...).
		WithLogf(func(format string, a ...any) { fmt.Printf(format+"\n", a...) })
	if err := runner.Run(context.Background()); err != nil {
		fatalCLI("install failed: %v", err)
	}
	fmt.Println("install complete")
}

// defaultWhisperURL is the medium.en q5_0 GGML model used for #10 audio QA.
const defaultWhisperURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-medium.en-q5_0.bin"
