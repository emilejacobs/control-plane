// Command cp-api is the Control Plane HTTP service: enrollment endpoint,
// device read endpoints, and (in later issues) auth + audit log surfaces.
//
// Required env:
//
//	DB_PASSWORD         Postgres password — injected from the RDS-managed
//	                    secret (issue #49). With DB_HOST (and optional
//	                    DB_PORT/DB_NAME/DB_USER/DB_SSLMODE, defaulting to
//	                    5432/uknomi_cp/uknomi_admin/require) the DSN is built
//	                    in-process. Or set DB_DSN with a full postgres:// URL.
//	IOT_POLICY_NAME     name of the IoT Core policy to attach to each device cert
//	JWT_SIGNING_KEY     base64-encoded HS256 signing key, >= 32 bytes decoded (ADR-010)
//	TOTP_ENCRYPTION_KEY base64-encoded AES-256 key, exactly 32 bytes decoded (TOTP secret at rest)
//
// Optional env:
//
//	PORT                    listen port (default 8080)
//	CP_BOOTSTRAP_SECRET_ID  Secrets Manager id of the bootstrap key (default uknomi/cp/bootstrap-key)
//	CORS_ALLOWED_ORIGINS    comma-separated allow list for the CORS middleware (default: empty = CORS disabled).
//	                        Production sets this to the dashboard origin (https://control.uknomi.com).
//	AWS_REGION              AWS region (default from default credentials chain)
//	AWS_ENDPOINT_URL        override the AWS service endpoint (dev/moto only)
//	TAXONOMY_ECS_CLUSTER    ECS cluster ARN for the cp-taxonomy-sync RunTask (ADR-033).
//	                        Unset disables POST /taxonomy/sync.
//	TAXONOMY_ECS_TASK_DEF   ECS task definition ARN/family for cp-taxonomy-sync.
//	TAXONOMY_ECS_SUBNETS    comma-separated private subnet ids.
//	TAXONOMY_ECS_SGS        comma-separated security group ids.
//	AGENT_DIST_BUCKET       S3 bucket holding the signed agent release catalog
//	                        (issue #40). Unset disables POST /agent-rollouts.
//	CP_COMMAND_SIGNING_SECRET_ID  Secrets Manager id of the base64 Ed25519
//	                        command-signing private key (issue #41). Unset =
//	                        agent.update published unsigned (forward-compat).
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
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/aws/aws-sdk-go-v2/service/iotdataplane"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/agentrollout"
	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/devices"
	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/fleet"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/cp/bootstrap"
	"github.com/emilejacobs/control-plane/internal/cp/captures"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/cp/commission"
	"github.com/emilejacobs/control-plane/internal/cp/iotpublisher"
	"github.com/emilejacobs/control-plane/internal/cp/tailscale"
	"github.com/google/uuid"
	"github.com/emilejacobs/control-plane/internal/cp/operators"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
	"github.com/emilejacobs/control-plane/internal/protocol/cmdsign"
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
	dsn, err := storage.ResolveDSN(os.Getenv)
	if err != nil {
		return fmt.Errorf("resolve db dsn: %w", err)
	}
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
	var iotDataOpts []func(*iotdataplane.Options)
	var smOpts []func(*secretsmanager.Options)
	var ecsOpts []func(*ecs.Options)
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		logger.Info("AWS_ENDPOINT_URL override active", "endpoint", endpoint)
		iotOpts = append(iotOpts, func(o *iot.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
		iotDataOpts = append(iotDataOpts, func(o *iotdataplane.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
		smOpts = append(smOpts, func(o *secretsmanager.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
		ecsOpts = append(ecsOpts, func(o *ecs.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	iotClient := iot.NewFromConfig(awsCfg, iotOpts...)
	// IoT Data Plane has a region-derived endpoint
	// (data.iot.<region>.amazonaws.com) — the SDK resolves it from
	// awsCfg unless overridden. Phase 2 slice 2 uses it for the
	// PUT /devices/{id}/service-config → config.update publish.
	iotDataClient := iotdataplane.NewFromConfig(awsCfg, iotDataOpts...)
	cmdPublisher := iotpublisher.NewAWS(iotDataClient)

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

	// Taxonomy mirror surface (ADR-033). Always wire the store so
	// GET /taxonomy/status works; only wire the RunTask invoker when
	// the ECS env vars are all set — until Terraform creates the task
	// def, the POST /taxonomy/sync route stays disabled.
	taxonomyStore := taxonomy.NewStore(pool)
	var taxonomyRunTask api.RunTaskInvoker
	if cluster := os.Getenv("TAXONOMY_ECS_CLUSTER"); cluster != "" {
		ecsClient := ecs.NewFromConfig(awsCfg, ecsOpts...)
		taxonomyRunTask = taxonomy.NewAWSRunTaskInvoker(ecsClient, taxonomy.RunTaskConfig{
			Cluster:        cluster,
			TaskDefinition: mustEnv("TAXONOMY_ECS_TASK_DEF"),
			Subnets:        csvEnv("TAXONOMY_ECS_SUBNETS"),
			SecurityGroups: csvEnv("TAXONOMY_ECS_SGS"),
		})
		logger.Info("taxonomy RunTask wired", "task_def", os.Getenv("TAXONOMY_ECS_TASK_DEF"))
	} else {
		logger.Info("taxonomy RunTask disabled — TAXONOMY_ECS_CLUSTER unset")
	}

	// Captures presigner (#8). Gated on CAPTURES_BUCKET so the signed-URL
	// route stays off until Terraform provisions the bucket; the rest of the
	// captures surface still serves.
	var capturePresigner captures.Presigner
	if bucket := os.Getenv("CAPTURES_BUCKET"); bucket != "" {
		capturePresigner = captures.NewS3Presigner(s3.NewPresignClient(s3.NewFromConfig(awsCfg)), bucket)
		logger.Info("captures presigner wired", "bucket", bucket)
	} else {
		logger.Info("captures presigner disabled — CAPTURES_BUCKET unset")
	}

	// Agent fleet-update (#40). Gated on AGENT_DIST_BUCKET — until
	// Terraform grants cp-api read access to the release catalog,
	// POST /agent-rollouts stays disabled.
	var rolloutCatalog agentrollout.ManifestSource
	var rolloutPusher devices.UpdatePusher
	// versionCatalog stays a true nil interface until the bucket is configured,
	// so the GET /fleet/agent-versions route guard (d.AgentVersionCatalog !=
	// nil) sees nil rather than a typed-nil *S3ManifestSource.
	var versionCatalog fleet.VersionCatalog
	if bucket := os.Getenv("AGENT_DIST_BUCKET"); bucket != "" {
		s3Client := s3.NewFromConfig(awsCfg)
		s3Catalog := agentrollout.NewS3ManifestSource(s3Client, bucket)
		rolloutCatalog = s3Catalog
		versionCatalog = s3Catalog
		// Command-envelope signing (#41). Gated on CP_COMMAND_SIGNING_SECRET_ID
		// so a deploy before the secret exists still serves the rollout API
		// (unsigned, ADR-028 forward-compat); a verifying agent rejects an
		// unsigned agent.update, so production sets both together.
		cmdSigner, err := loadCommandSigner(ctx, smClient, logger)
		if err != nil {
			return err
		}
		rolloutPusher = &agentrollout.Pusher{
			Manifests: rolloutCatalog,
			Presigner: agentrollout.NewS3Presigner(s3.NewPresignClient(s3Client), bucket),
			Publisher: cmdPublisher,
			Logger:    logger,
			Signer:    cmdSigner,
		}
		logger.Info("agent rollout surface wired", "bucket", bucket)
	} else {
		logger.Info("agent rollout surface disabled — AGENT_DIST_BUCKET unset")
	}

	// Commission surface (#91). Gated on TAILSCALE_API_SECRET_ID — a deploy
	// before the Tailscale credential exists keeps serving with the route
	// disabled. Reuses the cmd publisher to push cameras + the commission cmd.
	var commissioner devices.Commissioner
	if tsSecretID := os.Getenv("TAILSCALE_API_SECRET_ID"); tsSecretID != "" {
		token, err := bootstrap.NewSecretsManagerLoader(smClient, tsSecretID).Load(ctx)
		if err != nil {
			logger.Error("load tailscale api credential", "error", err)
			return err
		}
		tailnet := os.Getenv("TAILSCALE_TAILNET")
		tags := csvEnv("TAILSCALE_DEVICE_TAGS")
		if len(tags) == 0 {
			tags = []string{"tag:edge-device"}
		}
		commissioner = commission.New(reg, tailscale.NewClient(token, tailnet), cmdPublisher,
			commission.Config{Tailnet: tailnet, TailscaleTags: tags, TailscaleExpirySeconds: 3600},
			func() string { return uuid.NewString() })
		logger.Info("commission surface wired", "tailnet", tailnet)
	} else {
		logger.Info("commission surface disabled — TAILSCALE_API_SECRET_ID unset")
	}

	srv := &http.Server{
		Addr: ":" + port,
		Handler: api.NewRouter(api.Deps{
			Registry:           reg,
			AuthN:              authnSvc,
			AuthZ:              authzSvc,
			Operators:          operators.New(pool),
			CapturePresigner:   capturePresigner,
			IdempotencyStore:   idemStore,
			TaxonomyStore:      taxonomyStore,
			TaxonomyRunTask:    taxonomyRunTask,
			Audit:              auditW,
			Logger:             logger,
			CORSAllowedOrigins: csvEnv("CORS_ALLOWED_ORIGINS"),
			CmdPublisher:       cmdPublisher,
			Commissioner:       commissioner,
			AgentRolloutCatalog: rolloutCatalog,
			AgentRolloutPusher:  rolloutPusher,
			AgentVersionCatalog: versionCatalog,
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

// loadCommandSigner builds the agent.update command signer from the Ed25519
// private key in Secrets Manager (issue #41). Returns a nil signer (unsigned,
// ADR-028 forward-compat) when CP_COMMAND_SIGNING_SECRET_ID is unset; a
// configured-but-unloadable key fails startup rather than silently signing
// nothing. Returns the interface type so an unset key is a true nil (not a
// typed-nil that would trip the Pusher's signer check).
func loadCommandSigner(ctx context.Context, smClient *secretsmanager.Client, logger *slog.Logger) (agentrollout.CommandSigner, error) {
	secretID := os.Getenv("CP_COMMAND_SIGNING_SECRET_ID")
	if secretID == "" {
		logger.Info("command signing disabled — CP_COMMAND_SIGNING_SECRET_ID unset")
		return nil, nil
	}
	signer, err := cmdsign.LoadSigner(ctx, bootstrap.NewSecretsManagerLoader(smClient, secretID))
	if err != nil {
		return nil, fmt.Errorf("command signing key: %w", err)
	}
	logger.Info("command signing wired", "secret_id", secretID)
	return signer, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// csvEnv reads a comma-separated env var and returns the trimmed,
// non-empty entries. An unset var returns nil so a downstream "empty
// disables" check (CORSAllowedOrigins) reads naturally.
func csvEnv(name string) []string {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
