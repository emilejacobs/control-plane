// Command cp-ingest is the Control Plane presence ingest worker. It runs
// three components against one shared in-memory Presence model: a heartbeat
// SQS consumer (updates last_seen), a lifecycle SQS consumer (IoT
// connect/disconnect → is_online), and a sweeper goroutine (stale devices →
// offline). It runs as a Fargate service (ADR-018).
//
// Required env:
//
//	DB_PASSWORD          Postgres password from the RDS-managed secret (#49);
//	                     with DB_HOST (+ optional DB_PORT/DB_NAME/DB_USER/
//	                     DB_SSLMODE) the DSN is built in-process. Or set DB_DSN.
//	HEARTBEAT_QUEUE_URL  SQS URL of the cp-presence-heartbeats queue
//	HEARTBEAT_DLQ_URL    SQS URL of its dead-letter queue
//	LIFECYCLE_QUEUE_URL  SQS URL of the cp-presence-lifecycle queue
//	LIFECYCLE_DLQ_URL    SQS URL of its dead-letter queue
//
// Optional env:
//
//	AWS_REGION                 AWS region (default from the credentials chain)
//	AWS_ENDPOINT_URL           override the AWS service endpoint (dev/moto only)
//	SERVICE_STATUS_QUEUE_URL   SQS URL of the service-status queue (Phase 2)
//	SERVICE_STATUS_DLQ_URL     SQS URL of the service-status DLQ (Phase 2)
//	CMD_RESULT_QUEUE_URL       SQS URL of the cmd-result queue (Phase 2 slice 2)
//	CMD_RESULT_DLQ_URL         SQS URL of the cmd-result DLQ (Phase 2 slice 2)
//	SERVICE_STATUS_DLQ_URL     dead-letter queue for service-status (Phase 2)
//	AGENT_DIST_BUCKET          S3 bucket holding the signed agent release
//	                           catalog (issue #40). When set, the heartbeat +
//	                           lifecycle consumers re-push agent.update to
//	                           devices whose reported version drifted from
//	                           desired_agent_version. Unset disables the
//	                           reconcile (reported versions still persist).
//	CP_COMMAND_SIGNING_SECRET_ID  Secrets Manager id of the base64 Ed25519
//	                           command-signing key (issue #41). When set, the
//	                           reconcile re-pushes are signed; unset = unsigned
//	                           (forward-compat). Set it together with cp-api's.
//	CAPTURES_BUCKET            S3 bucket for the captures pipeline (issue #8).
//	                           When set, the cmd-result consumer handles
//	                           upload.request (presign a PUT + publish
//	                           upload.url) and upload.complete (index the row).
//	                           Unset = those message types are ignored.
//
// SERVICE_STATUS_* are optional so a deploy that lands the code before
// Terraform provisions the queue does not crash. When both are set, the
// service-status consumer joins the heartbeat + lifecycle consumers; when
// either is missing, the consumer is silently skipped and the rest of
// cp-ingest runs as before.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iotdataplane"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/agentrollout"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/bootstrap"
	"github.com/emilejacobs/control-plane/internal/cp/captures"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/iotpublisher"
	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
	"github.com/emilejacobs/control-plane/internal/protocol/cmdsign"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
	"github.com/emilejacobs/control-plane/internal/protocol/servicestatus"
)

func main() {
	logger := cplog.New(os.Stdout, "cp-ingest")
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("cp-ingest exited", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dsn, err := storage.ResolveDSN(os.Getenv)
	if err != nil {
		return fmt.Errorf("resolve db dsn: %w", err)
	}
	heartbeatQueueURL := mustEnv("HEARTBEAT_QUEUE_URL")
	heartbeatDLQURL := mustEnv("HEARTBEAT_DLQ_URL")
	lifecycleQueueURL := mustEnv("LIFECYCLE_QUEUE_URL")
	lifecycleDLQURL := mustEnv("LIFECYCLE_DLQ_URL")

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
	var sqsOpts []func(*sqs.Options)
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		logger.Info("AWS_ENDPOINT_URL override active", "endpoint", endpoint)
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	sqsClient := sqs.NewFromConfig(awsCfg, sqsOpts...)

	// cp-ingest only reads/updates devices; it never enrolls one, so the
	// registry's IoT provisioner and bootstrap key are unused here.
	reg := registry.New(pool, nil, registry.Config{})

	// One shared in-memory Presence model: heartbeats and lifecycle events
	// feed it, the sweeper reads it.
	pres := presence.New()

	auditW := audit.NewPostgresWriter(pool)

	heartbeatIngester := ingest.NewPresenceIngester(pres, reg, nil)
	lifecycleIngester := ingest.NewLifecycleIngester(pres, reg, nil)

	// Agent fleet-update reconcile (#40): when the agent-dist bucket is
	// configured, a heartbeat or reconnect from a device whose reported
	// version drifted from desired_agent_version re-pushes agent.update.
	// Unset = reconcile disabled (the reported version still persists);
	// keeps deploys that land before Terraform grants the access serving.
	if bucket := os.Getenv("AGENT_DIST_BUCKET"); bucket != "" {
		s3Client := s3.NewFromConfig(awsCfg)
		var iotDataOpts []func(*iotdataplane.Options)
		if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
			iotDataOpts = append(iotDataOpts, func(o *iotdataplane.Options) {
				o.BaseEndpoint = aws.String(endpoint)
			})
		}
		// Command-envelope signing (#41): reconcile re-pushes must be signed
		// too, or a verifying agent rejects them. Gated on
		// CP_COMMAND_SIGNING_SECRET_ID, same as cp-api; unset = unsigned
		// (forward-compat).
		var cmdSigner agentrollout.CommandSigner
		if secretID := os.Getenv("CP_COMMAND_SIGNING_SECRET_ID"); secretID != "" {
			var smOpts []func(*secretsmanager.Options)
			if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
				smOpts = append(smOpts, func(o *secretsmanager.Options) { o.BaseEndpoint = aws.String(endpoint) })
			}
			smClient := secretsmanager.NewFromConfig(awsCfg, smOpts...)
			signer, err := cmdsign.LoadSigner(ctx, bootstrap.NewSecretsManagerLoader(smClient, secretID))
			if err != nil {
				return fmt.Errorf("command signing key: %w", err)
			}
			cmdSigner = signer
			logger.Info("command signing wired", "secret_id", secretID)
		} else {
			logger.Info("command signing disabled — CP_COMMAND_SIGNING_SECRET_ID unset")
		}
		pusher := &agentrollout.Pusher{
			Manifests: agentrollout.NewS3ManifestSource(s3Client, bucket),
			Presigner: agentrollout.NewS3Presigner(s3.NewPresignClient(s3Client), bucket),
			Publisher: iotpublisher.NewAWS(iotdataplane.NewFromConfig(awsCfg, iotDataOpts...)),
			Logger:    logger,
			Signer:    cmdSigner,
		}
		heartbeatIngester.Updates = pusher
		heartbeatIngester.Logger = logger
		lifecycleIngester.Versions = reg
		lifecycleIngester.Updates = pusher
		lifecycleIngester.Logger = logger
		logger.Info("agent-update reconcile wired", "bucket", bucket)
	} else {
		logger.Info("agent-update reconcile disabled — AGENT_DIST_BUCKET unset")
	}

	heartbeatConsumer := sqsconsumer.NewConsumer[ingest.Heartbeat](
		sqsClient,
		heartbeatIngester.Handle,
		sqsconsumer.Config{QueueURL: heartbeatQueueURL, DLQURL: heartbeatDLQURL, Logger: logger, Audit: auditW},
	)
	lifecycleConsumer := sqsconsumer.NewConsumer[ingest.Lifecycle](
		sqsClient,
		lifecycleIngester.Handle,
		sqsconsumer.Config{QueueURL: lifecycleQueueURL, DLQURL: lifecycleDLQURL, Logger: logger, Audit: auditW},
	)
	sweeper := ingest.NewPresenceSweeper(pres, reg, ingest.SweeperConfig{Logger: logger})

	// Phase 2 slice 3: log-tail row sweeper. Default 1h cadence, 24h
	// retention (per PRD). Independent of presence sweeper — separate
	// goroutine, separate cadence.
	logTailSweeper := ingest.NewLogTailSweeper(reg, ingest.LogTailSweeperConfig{Logger: logger})

	// Phase 2 followups #01: device_services sweeper. Default 10min
	// cadence, 15min stale threshold (= 3× the 5min service-status
	// cadence). Drops rows for services an operator removed from the
	// device's allow-list — without this, the Services panel shows
	// the removed service forever (RecordServiceStates is per-service
	// UPSERT, not replace-all-per-device, by design).
	deviceServicesSweeper := ingest.NewDeviceServicesSweeper(reg, ingest.DeviceServicesSweeperConfig{Logger: logger})

	// Optional service-status consumer (Phase 2). Skipped silently if
	// the env vars aren't set yet — lets the code deploy before
	// Terraform provisions the queue.
	var serviceStatusConsumer *sqsconsumer.Consumer[servicestatus.Report]
	serviceStatusQueueURL := os.Getenv("SERVICE_STATUS_QUEUE_URL")
	serviceStatusDLQURL := os.Getenv("SERVICE_STATUS_DLQ_URL")
	if serviceStatusQueueURL != "" && serviceStatusDLQURL != "" {
		ssIngester := ingest.NewServiceStatusIngester(reg, nil)
		// Surface stopped-service log lines so the Phase 2 alarm's
		// log-metric-filter can count them.
		ssIngester.Logger = logger
		serviceStatusConsumer = sqsconsumer.NewConsumer[servicestatus.Report](
			sqsClient,
			ssIngester.Handle,
			sqsconsumer.Config{QueueURL: serviceStatusQueueURL, DLQURL: serviceStatusDLQURL, Logger: logger, Audit: auditW},
		)
	}

	// Optional health-probes consumer (Phase 2, issue #19). Skipped
	// silently until Terraform provisions the queue, same posture as the
	// service-status consumer.
	var healthProbeConsumer *sqsconsumer.Consumer[healthprobes.Report]
	healthProbesQueueURL := os.Getenv("HEALTH_PROBES_QUEUE_URL")
	healthProbesDLQURL := os.Getenv("HEALTH_PROBES_DLQ_URL")
	if healthProbesQueueURL != "" && healthProbesDLQURL != "" {
		hpIngester := ingest.NewHealthProbeIngester(reg, nil)
		// Surface red-probe log lines so the per-probe-type alarm's
		// log-metric-filter can count them.
		hpIngester.Logger = logger
		healthProbeConsumer = sqsconsumer.NewConsumer[healthprobes.Report](
			sqsClient,
			hpIngester.Handle,
			sqsconsumer.Config{QueueURL: healthProbesQueueURL, DLQURL: healthProbesDLQURL, Logger: logger, Audit: auditW},
		)
	}

	// Optional cmd-result consumer (Phase 2 slice 2). Routes config.update
	// ACKs to the registry's RecordServiceConfigApplied. Skipped silently
	// while the queue / IoT Rule are being provisioned, same posture as
	// the service-status consumer above.
	var cmdResultConsumer *sqsconsumer.Consumer[ingest.CmdResult]
	cmdResultQueueURL := os.Getenv("CMD_RESULT_QUEUE_URL")
	cmdResultDLQURL := os.Getenv("CMD_RESULT_DLQ_URL")
	if cmdResultQueueURL != "" && cmdResultDLQURL != "" {
		crIngester := ingest.NewCmdResultIngester(reg, nil)
		crIngester.Logger = logger
		// Captures upload pipeline (#8): gated on CAPTURES_BUCKET. When set,
		// upload.request presigns a PUT against the captures bucket and
		// publishes upload.url back on the device cmd topic; upload.complete
		// indexes the row. Unset → the cmd-result handler ignores those types.
		if capturesBucket := os.Getenv("CAPTURES_BUCKET"); capturesBucket != "" {
			capS3 := s3.NewFromConfig(awsCfg)
			var capIotOpts []func(*iotdataplane.Options)
			if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
				capIotOpts = append(capIotOpts, func(o *iotdataplane.Options) {
					o.BaseEndpoint = aws.String(endpoint)
				})
			}
			crIngester.Captures = reg
			crIngester.Presigner = captures.NewS3Presigner(s3.NewPresignClient(capS3), capturesBucket)
			crIngester.Publisher = iotpublisher.NewAWS(iotdataplane.NewFromConfig(awsCfg, capIotOpts...))
			logger.Info("captures upload pipeline enabled", "captures_bucket", capturesBucket)
		} else {
			logger.Info("captures upload pipeline disabled — CAPTURES_BUCKET unset")
		}
		cmdResultConsumer = sqsconsumer.NewConsumer[ingest.CmdResult](
			sqsClient,
			crIngester.Handle,
			sqsconsumer.Config{QueueURL: cmdResultQueueURL, DLQURL: cmdResultDLQURL, Logger: logger, Audit: auditW},
		)
	}

	logger.Info("cp-ingest starting",
		"heartbeat_queue", heartbeatQueueURL,
		"lifecycle_queue", lifecycleQueueURL,
		"service_status_queue", serviceStatusQueueURL,
		"service_status_enabled", serviceStatusConsumer != nil,
		"health_probes_queue", healthProbesQueueURL,
		"health_probes_enabled", healthProbeConsumer != nil,
		"cmd_result_queue", cmdResultQueueURL,
		"cmd_result_enabled", cmdResultConsumer != nil)

	// Run all consumers + the sweeper until the signal context is cancelled,
	// then wait for a clean drain. The consumers report drain errors; the
	// sweeper does not.
	var wg sync.WaitGroup
	workers := 5 // heartbeat + lifecycle + presence sweeper + log-tail sweeper + device-services sweeper
	if serviceStatusConsumer != nil {
		workers++
	}
	if healthProbeConsumer != nil {
		workers++
	}
	if cmdResultConsumer != nil {
		workers++
	}
	errs := make(chan error, workers)
	wg.Add(workers)
	go func() { defer wg.Done(); errs <- heartbeatConsumer.Run(ctx) }()
	go func() { defer wg.Done(); errs <- lifecycleConsumer.Run(ctx) }()
	go func() { defer wg.Done(); sweeper.Run(ctx) }()
	go func() { defer wg.Done(); logTailSweeper.Run(ctx) }()
	go func() { defer wg.Done(); deviceServicesSweeper.Run(ctx) }()
	if serviceStatusConsumer != nil {
		go func() { defer wg.Done(); errs <- serviceStatusConsumer.Run(ctx) }()
	}
	if healthProbeConsumer != nil {
		go func() { defer wg.Done(); errs <- healthProbeConsumer.Run(ctx) }()
	}
	if cmdResultConsumer != nil {
		go func() { defer wg.Done(); errs <- cmdResultConsumer.Run(ctx) }()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			return fmt.Errorf("worker: %w", err)
		}
	}
	logger.Info("cp-ingest stopped cleanly")
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
