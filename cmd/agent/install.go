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

	runner := install.BuildRunner(install.NewOSSystem(), plan).
		WithLogf(func(format string, a ...any) { fmt.Printf(format+"\n", a...) })
	if err := runner.Run(context.Background()); err != nil {
		fatalCLI("install failed: %v", err)
	}
	fmt.Println("install complete")
}
