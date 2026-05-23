// Command cp-api is the Control Plane HTTP service: enrollment endpoint,
// device read endpoints, and (in later issues) auth + audit log surfaces.
//
// Required env:
//
//	DB_DSN              Postgres DSN (postgres://...)
//	IOT_POLICY_NAME     name of the IoT Core policy to attach to each device cert
//	JWT_SIGNING_KEY     base64-encoded HS256 signing key, >= 32 bytes decoded (ADR-010)
//	TOTP_ENCRYPTION_KEY base64-encoded AES-256 key, exactly 32 bytes decoded (TOTP secret at rest)
//
// Optional env:
//
//	PORT                    listen port (default 8080)
//	CP_BOOTSTRAP_SECRET_ID  Secrets Manager id of the bootstrap key (default uknomi/cp/bootstrap-key)
//	AWS_REGION              AWS region (default from default credentials chain)
//	AWS_ENDPOINT_URL        override the AWS service endpoint (dev/moto only)
package main

import (
	"context"
	"encoding/base64"
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
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/cp/bootstrap"
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
	policyName := mustEnv("IOT_POLICY_NAME")
	bootstrapSecretID := envOr("CP_BOOTSTRAP_SECRET_ID", "uknomi/cp/bootstrap-key")
	port := envOr("PORT", "8080")

	signingKey, err := base64.StdEncoding.DecodeString(mustEnv("JWT_SIGNING_KEY"))
	if err != nil {
		return fmt.Errorf("JWT_SIGNING_KEY is not valid base64: %w", err)
	}
	if len(signingKey) < 32 {
		return fmt.Errorf("JWT_SIGNING_KEY must decode to at least 32 bytes, got %d", len(signingKey))
	}

	totpKey, err := base64.StdEncoding.DecodeString(mustEnv("TOTP_ENCRYPTION_KEY"))
	if err != nil {
		return fmt.Errorf("TOTP_ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(totpKey) != 32 {
		return fmt.Errorf("TOTP_ENCRYPTION_KEY must decode to exactly 32 bytes, got %d", len(totpKey))
	}

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
	var smOpts []func(*secretsmanager.Options)
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		logger.Info("AWS_ENDPOINT_URL override active", "endpoint", endpoint)
		iotOpts = append(iotOpts, func(o *iot.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
		smOpts = append(smOpts, func(o *secretsmanager.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	iotClient := iot.NewFromConfig(awsCfg, iotOpts...)

	// The bootstrap key's store of record is Secrets Manager (ADR-017). The
	// verifier loads it eagerly — a key store it cannot reach fails startup.
	smClient := secretsmanager.NewFromConfig(awsCfg, smOpts...)
	bootstrapLoader := bootstrap.NewSecretsManagerLoader(smClient, bootstrapSecretID)
	bootstrapVerifier, err := bootstrap.NewVerifier(ctx, bootstrapLoader)
	if err != nil {
		return fmt.Errorf("bootstrap key: %w", err)
	}
	logger.Info("bootstrap key loaded", "secret_id", bootstrapSecretID)

	prov := iotprovisioner.NewAWS(iotClient, policyName)
	reg := registry.New(pool, prov, registry.Config{BootstrapVerifier: bootstrapVerifier})
	idemStore := storage.NewIdempotencyStore(pool)
	authnSvc := authn.New(pool, authn.Config{SigningKey: signingKey, TotpEncryptionKey: totpKey})
	authzSvc := authz.New(pool)
	auditW := audit.NewPostgresWriter(pool)

	srv := &http.Server{
		Addr: ":" + port,
		Handler: api.NewRouter(api.Deps{
			Registry:         reg,
			AuthN:            authnSvc,
			AuthZ:            authzSvc,
			IdempotencyStore: idemStore,
			Audit:            auditW,
			Logger:           logger,
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
