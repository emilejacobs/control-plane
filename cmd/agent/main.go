package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emilejacobs/control-plane/internal/agent"
	"github.com/emilejacobs/control-plane/internal/config"
	"github.com/emilejacobs/control-plane/internal/probes"
	"github.com/emilejacobs/control-plane/internal/service"
	"github.com/emilejacobs/control-plane/internal/transport"
)

// version is stamped at build time via -ldflags "-X main.version=<v>"
// (agent-release.yml). It is the agent's source of truth for its running
// version, which MUST survive a self-update: the config file's version is
// static, so a self-updated binary that read it would report the old version
// forever and CP would re-push the update in a loop (issue #39, ADR-035 §1).
// Empty in dev/local builds, where the config file's version is the fallback.
var version string

func main() {
	// Subcommand dispatch. The daemon path is invoked as `uknomi-agent
	// --config ...` (by the LaunchDaemon/supervisor), so anything starting
	// with a flag falls through to runDaemon and the existing behaviour is
	// preserved. `enroll` is the one-shot device-side enrollment (#82); the
	// full `install` subcommand (ADR-037) lands with #86.
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "install":
			runInstall(os.Args[2:])
			return
		case "enroll":
			runEnroll(os.Args[2:])
			return
		}
	}
	runDaemon()
}

func runDaemon() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	// launchd's default PATH is minimal; nmap (network.scan) and docker
	// (log.tail docker kind) ship under Homebrew's prefix.
	agent.AugmentSubprocessPath()

	configPath := flag.String("config", "", "path to JSON config file (required)")
	flag.Parse()

	if *configPath == "" {
		logger.Error("--config is required")
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	// The build-stamped version wins over the (static) config version so a
	// self-updated binary reports its true version; config is the fallback
	// for dev/local builds with no ldflags stamp.
	agentVersion := version
	if agentVersion == "" {
		agentVersion = cfg.Version
	}

	caPEM, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
		logger.Error("read CA cert", "path", cfg.CACertPath, "error", err)
		os.Exit(1)
	}
	certPEM, err := os.ReadFile(cfg.CertPath)
	if err != nil {
		logger.Error("read cert", "path", cfg.CertPath, "error", err)
		os.Exit(1)
	}
	keyPEM, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		logger.Error("read key", "path", cfg.KeyPath, "error", err)
		os.Exit(1)
	}

	tr, err := transport.New(transport.Config{
		BrokerURL: cfg.BrokerURL,
		ClientID:  cfg.ClientID,
		CACertPEM: caPEM,
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
	})
	if err != nil {
		logger.Error("transport", "error", err)
		os.Exit(1)
	}

	var telemetryInterval time.Duration
	if cfg.TelemetryInterval != "" {
		d, err := time.ParseDuration(cfg.TelemetryInterval)
		if err != nil {
			logger.Error("parse telemetry_interval", "value", cfg.TelemetryInterval, "error", err)
			os.Exit(1)
		}
		telemetryInterval = d
	}
	var serviceStatusInterval time.Duration
	if cfg.ServiceStatusInterval != "" {
		d, err := time.ParseDuration(cfg.ServiceStatusInterval)
		if err != nil {
			logger.Error("parse service_status_interval", "value", cfg.ServiceStatusInterval, "error", err)
			os.Exit(1)
		}
		serviceStatusInterval = d
	}

	var probeInterval time.Duration
	if cfg.ProbeInterval != "" {
		d, err := time.ParseDuration(cfg.ProbeInterval)
		if err != nil {
			logger.Error("parse probe_interval", "value", cfg.ProbeInterval, "error", err)
			os.Exit(1)
		}
		probeInterval = d
	}

	var cameraProbeInterval time.Duration
	if cfg.CameraProbeInterval != "" {
		d, err := time.ParseDuration(cfg.CameraProbeInterval)
		if err != nil {
			logger.Error("parse camera_probe_interval", "value", cfg.CameraProbeInterval, "error", err)
			os.Exit(1)
		}
		cameraProbeInterval = d
	}

	a, err := agent.New(agent.Config{
		CertPath:              cfg.CertPath,
		DeviceID:              cfg.DeviceID,
		Version:               agentVersion,
		TelemetryInterval:     telemetryInterval,
		ServiceAllowList:      cfg.ServiceAllowList,
		ServiceStatusInterval: serviceStatusInterval,
		ProbeInterval:         probeInterval,
		CameraProbeInterval:   cameraProbeInterval,
		ConfigPath:            *configPath,
		CamerasPath:           cfg.CamerasPath,
		SnapshotStatePath:     cfg.SnapshotStatePath,
		AutoLoginUser:         cfg.AutoLoginUser,
		// AGENT_DIR is exported by the resident wrapper
		// (scripts/uknomi-agent-supervisor.sh). When present, the agent
		// enables the signature-gated agent.update handler and writes the
		// health marker the wrapper gates a candidate on (issue #39).
		UpdateDir: os.Getenv("AGENT_DIR"),
	}, tr,
		agent.WithLogger(logger),
		agent.WithServiceBackend(service.NewSystemBackend(logger)),
		agent.WithProbeBackend(probes.NewSystemBackend(cfg.AutoLoginUser, logger)),
	)
	if err != nil {
		logger.Error("agent", "error", err)
		os.Exit(1)
	}

	if err := a.Start(); err != nil {
		logger.Error("start", "error", err)
		os.Exit(1)
	}
	logger.Info("agent started", "device_id", cfg.DeviceID, "version", agentVersion, "broker_url", cfg.BrokerURL)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig.String())
	case <-a.RestartRequested():
		// A staged update asked us to exit so the resident wrapper restarts
		// and health-gates the candidate (issue #39, ADR-035 §3). Exit 0 —
		// this is an orderly hand-off, not a crash.
		logger.Info("shutting down to let the wrapper gate a staged update")
	case <-a.WedgeDetected():
		// The transport watchdog confirmed a dead MQTT session (#65). Exit
		// non-zero so launchd's KeepAlive restarts us through the supervisor
		// with a fresh transport — the proven recovery (manual kickstart).
		// Stop is best-effort: a wedged paho client may not close cleanly.
		logger.Error("mqtt session wedged — exiting so launchd restarts the agent with a fresh transport")
		_ = a.Stop()
		os.Exit(1)
	}

	if err := a.Stop(); err != nil {
		logger.Error("stop", "error", err)
		os.Exit(1)
	}
}
