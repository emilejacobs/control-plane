// Command cp-api is the Control Plane HTTP service: enrollment endpoint,
// device read endpoints, and (in later issues) auth + audit log surfaces.
//
// Required env:
//
//	DB_DSN              Postgres DSN (postgres://...)
//	CP_BOOTSTRAP_KEY    bootstrap key the install script presents (#10 swaps this for Secrets Manager)
//	IOT_POLICY_NAME     name of the IoT Core policy to attach to each device cert
//
// Optional env:
//
//	PORT                listen port (default 8080)
//	AWS_REGION          AWS region (default from default credentials chain)
//	AWS_ENDPOINT_URL    override the AWS service endpoint (dev/moto only)
//	CP_DEV_DEVICES_GET  "true" to expose GET /devices/{id} without auth (Issue 03 dev escape; removed in #04)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

func main() {
	logger := cplog.New(os.Stdout, "cp-api")
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("cp-api exited", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dsn := mustEnv("DB_DSN")
	bootstrapKey := mustEnv("CP_BOOTSTRAP_KEY")
	policyName := mustEnv("IOT_POLICY_NAME")
	port := envOr("PORT", "8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	if err := storage.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info("migrations applied")

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("aws config: %w", err)
	}
	var iotOpts []func(*iot.Options)
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		logger.Info("AWS_ENDPOINT_URL override active", "endpoint", endpoint)
		iotOpts = append(iotOpts, func(o *iot.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	iotClient := iot.NewFromConfig(awsCfg, iotOpts...)

	prov := iotprovisioner.NewAWS(iotClient, policyName)
	reg := registry.New(pool, prov, registry.Config{BootstrapKey: bootstrapKey})
	idemStore := storage.NewIdempotencyStore(pool)

	devDevicesGet := os.Getenv("CP_DEV_DEVICES_GET") == "true"
	if devDevicesGet {
		logger.Warn("CP_DEV_DEVICES_GET enabled — GET /devices/{id} is unauthenticated")
	}

	srv := &http.Server{
		Addr: ":" + port,
		Handler: api.NewRouter(api.Deps{
			Registry:             reg,
			IdempotencyStore:     idemStore,
			Logger:               logger,
			DevDevicesGetEnabled: devDevicesGet,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("cp-api stopped cleanly")
	return nil
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env var %s is not set\n", name)
		os.Exit(2)
	}
	return v
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
