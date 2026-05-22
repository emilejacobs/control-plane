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
//	AWS_REGION           AWS region (default from the credentials chain)
//	AWS_ENDPOINT_URL     override the AWS service endpoint (dev/moto only)
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

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
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

	heartbeatConsumer := sqsconsumer.NewConsumer[ingest.Heartbeat](
		sqsClient,
		ingest.NewPresenceIngester(pres, reg, nil).Handle,
		sqsconsumer.Config{QueueURL: heartbeatQueueURL, DLQURL: heartbeatDLQURL, Logger: logger},
	)
	lifecycleConsumer := sqsconsumer.NewConsumer[ingest.Lifecycle](
		sqsClient,
		ingest.NewLifecycleIngester(pres, reg, nil).Handle,
		sqsconsumer.Config{QueueURL: lifecycleQueueURL, DLQURL: lifecycleDLQURL, Logger: logger},
	)
	sweeper := ingest.NewPresenceSweeper(pres, reg, ingest.SweeperConfig{Logger: logger})

	logger.Info("cp-ingest starting",
		"heartbeat_queue", heartbeatQueueURL, "lifecycle_queue", lifecycleQueueURL)

	// Run all three until the signal context is cancelled, then wait for a
	// clean drain. The consumers report drain errors; the sweeper does not.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(3)
	go func() { defer wg.Done(); errs <- heartbeatConsumer.Run(ctx) }()
	go func() { defer wg.Done(); errs <- lifecycleConsumer.Run(ctx) }()
	go func() { defer wg.Done(); sweeper.Run(ctx) }()
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
