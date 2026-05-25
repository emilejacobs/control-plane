// Command cp-ingest is the Control Plane presence ingest worker. It runs
// three components against one shared in-memory Presence model: a heartbeat
// SQS consumer (updates last_seen), a lifecycle SQS consumer (IoT
// connect/disconnect → is_online), and a sweeper goroutine (stale devices →
// offline). It runs as a Fargate service (ADR-018).
//
// Required env:
//
//	DB_DSN               Postgres DSN (postgres://...)
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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
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
	dsn := mustEnv("DB_DSN")
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

	heartbeatConsumer := sqsconsumer.NewConsumer[ingest.Heartbeat](
		sqsClient,
		ingest.NewPresenceIngester(pres, reg, nil).Handle,
		sqsconsumer.Config{QueueURL: heartbeatQueueURL, DLQURL: heartbeatDLQURL, Logger: logger, Audit: auditW},
	)
	lifecycleConsumer := sqsconsumer.NewConsumer[ingest.Lifecycle](
		sqsClient,
		ingest.NewLifecycleIngester(pres, reg, nil).Handle,
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
