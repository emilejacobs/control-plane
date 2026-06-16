package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/emilejacobs/control-plane/internal/agent/enroll"
	"github.com/emilejacobs/control-plane/internal/config"
)

// runEnroll is the `uknomi-agent enroll` subcommand: gather the host's
// identity, present the bundled bootstrap key to POST /enrollments, and lay
// down the cert/key/ca + agent-config. Temporary entry point to exercise the
// enroll module on a real device (#82); it folds into `uknomi-agent install`
// with #86 (ADR-037).
func runEnroll(args []string) {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	cpURL := fs.String("cp-url", "https://api.control.uknomi.com", "CP base URL")
	brokerURL := fs.String("broker-url", "", "MQTT broker URL, e.g. tls://<ats-endpoint>:8883 (required)")
	runtimeDir := fs.String("runtime-dir", "/var/uknomi", "directory for cert/key/ca/agent-config")
	bootstrapKeyFile := fs.String("bootstrap-key-file", "", "path to the bootstrap key file (or set CP_BOOTSTRAP_KEY)")
	caFile := fs.String("ca-file", "", "path to the AWS IoT root CA PEM (required)")
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

	res, err := enroll.Enroll(context.Background(), enroll.Params{
		CPBaseURL:    *cpURL,
		BootstrapKey: bootstrapKey,
		Hardware:     gatherHardware(),
		CACertPEM:    caPEM,
		RuntimeDir:   *runtimeDir,
		BrokerURL:    *brokerURL,
		Defaults:     defaultAgentConfig(),
	})
	if err != nil {
		fatalCLI("enrollment failed: %v", err)
	}
	fmt.Printf("enrolled: device_id=%s config=%s\n", res.DeviceID, res.ConfigPath)
}

// defaultAgentConfig is the operational config every device starts with —
// the Go equivalent of the values the bash install module baked into
// agent-config.json. snapshot_state_path is included so scheduled snapshots
// (#9) activate on freshly-provisioned devices.
func defaultAgentConfig() config.Config {
	return config.Config{
		TelemetryInterval:     "30s",
		ServiceAllowList:      []string{"com.uknomi.edge-ui", "com.tailscale.tailscaled"},
		ServiceStatusInterval: "5m",
		CamerasPath:           "/usr/local/etc/uknomi/cameras.json",
		SnapshotStatePath:     "/var/uknomi/snapshot-state.json",
		ProbeInterval:         "5m",
		AutoLoginUser:         "uknomi",
	}
}

// gatherHardware reads the macOS host identity for the enrollment request.
// macOS-specific shell-outs (ioreg/sw_vers); the install slice (#86) moves
// these behind the ADR-034 backend split.
func gatherHardware() enroll.Hardware {
	host, _ := os.Hostname()
	agentVersion := version
	if agentVersion == "" {
		agentVersion = "dev"
	}
	return enroll.Hardware{
		Hostname:     host,
		HardwareUUID: ioregPlatformUUID(),
		HardwareKind: "mac",
		OSVersion:    "macOS " + productVersion(),
		AgentVersion: agentVersion,
	}
}

var ioPlatformUUIDRe = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

func ioregPlatformUUID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	if m := ioPlatformUUIDRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

func productVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func fatalCLI(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "uknomi-agent: "+format+"\n", a...)
	os.Exit(1)
}
