// Command cp-ingest is the Control Plane presence ingest worker: it consumes
// device heartbeats from SQS and updates devices.last_seen so the API can
// report freshness. It runs as a Fargate service (ADR-018).
//
// Required env:
//
//	DB_DSN               Postgres DSN (postgres://...)
//	HEARTBEAT_QUEUE_URL  SQS URL of the cp-presence-heartbeats queue
//	HEARTBEAT_DLQ_URL    SQS URL of its dead-letter queue
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
	queueURL := mustEnv("HEARTBEAT_QUEUE_URL")
	dlqURL := mustEnv("HEARTBEAT_DLQ_URL")

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

	// cp-ingest only updates last_seen; it never enrolls a device, so the
	// registry's IoT provisioner and bootstrap key are unused here.
	reg := registry.New(pool, nil, registry.Config{})
	ingester := ingest.NewPresenceIngester(presence.New(), reg, nil)
	consumer := sqsconsumer.NewConsumer[ingest.Heartbeat](sqsClient, ingester.Handle, sqsconsumer.Config{
		QueueURL: queueURL,
		DLQURL:   dlqURL,
		Logger:   logger,
	})

	logger.Info("cp-ingest consuming heartbeats", "queue", queueURL)
	if err := consumer.Run(ctx); err != nil {
		return fmt.Errorf("consumer: %w", err)
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
