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

func main() {
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

	a, err := agent.New(agent.Config{
		CertPath:              cfg.CertPath,
		DeviceID:              cfg.DeviceID,
		Version:               cfg.Version,
		TelemetryInterval:     telemetryInterval,
		ServiceAllowList:      cfg.ServiceAllowList,
		ServiceStatusInterval: serviceStatusInterval,
		ProbeInterval:         probeInterval,
		ConfigPath:            *configPath,
		CamerasPath:           cfg.CamerasPath,
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
	logger.Info("agent started", "device_id", cfg.DeviceID, "version", cfg.Version, "broker_url", cfg.BrokerURL)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logger.Info("shutting down", "signal", sig.String())

	if err := a.Stop(); err != nil {
		logger.Error("stop", "error", err)
		os.Exit(1)
	}
}
