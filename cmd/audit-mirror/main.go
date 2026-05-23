// Command audit-mirror exports audit_log rows to S3 as one gzipped JSON
// Lines object per UTC day. ADR-023: a short-lived Fargate task on an
// EventBridge schedule (00:05 UTC daily). Issue 28.
//
// Required env:
//
//	DB_DSN          Postgres DSN (postgres://...)
//	AUDIT_BUCKET    Name of the audit-mirror S3 bucket
//
// Optional env:
//
//	AWS_REGION       AWS region (default from default credentials chain)
//	AWS_ENDPOINT_URL Override the AWS S3 endpoint (dev/moto only)
//
// Flags (one mode per run):
//
//	--date YYYY-MM-DD          Export this single UTC day.
//	--from YYYY-MM-DD --to YYY Backfill the closed UTC-date range.
//	(no flags)                 Default: export the prior UTC day.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/auditmirror"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

func main() {
	logger := cplog.New(os.Stdout, "audit-mirror")
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("audit-mirror failed", "err", err)
		os.Exit(1)
	}
	logger.Info("audit-mirror completed")
}

func run(logger *slog.Logger) error {
	dateFlag := flag.String("date", "", "Export a single UTC day in YYYY-MM-DD form (default: yesterday).")
	fromFlag := flag.String("from", "", "Backfill start UTC date in YYYY-MM-DD form. Requires --to.")
	toFlag := flag.String("to", "", "Backfill end UTC date in YYYY-MM-DD form. Requires --from.")
	flag.Parse()

	dsn := mustEnv("DB_DSN")
	bucket := mustEnv("AUDIT_BUCKET")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("aws config: %w", err)
	}
	var s3Opts []func(*s3.Options)
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		logger.Info("AWS_ENDPOINT_URL override active", "endpoint", endpoint)
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	}
	s3Client := s3.NewFromConfig(awsCfg, s3Opts...)
	exporter := auditmirror.NewExporter(pool, s3Client, bucket)

	switch {
	case *fromFlag != "" || *toFlag != "":
		if *fromFlag == "" || *toFlag == "" {
			return fmt.Errorf("--from and --to must be set together")
		}
		from, err := time.Parse("2006-01-02", *fromFlag)
		if err != nil {
			return fmt.Errorf("--from: %w", err)
		}
		to, err := time.Parse("2006-01-02", *toFlag)
		if err != nil {
			return fmt.Errorf("--to: %w", err)
		}
		logger.Info("backfilling range", "from", *fromFlag, "to", *toFlag, "bucket", bucket)
		return exporter.ExportRange(ctx, from, to)
	case *dateFlag != "":
		day, err := time.Parse("2006-01-02", *dateFlag)
		if err != nil {
			return fmt.Errorf("--date: %w", err)
		}
		logger.Info("exporting single day", "date", *dateFlag, "bucket", bucket)
		return exporter.ExportDate(ctx, day)
	default:
		yesterday := time.Now().UTC().AddDate(0, 0, -1)
		logger.Info("exporting yesterday", "date", yesterday.Format("2006-01-02"), "bucket", bucket)
		return exporter.ExportDate(ctx, yesterday)
	}
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env var %s is not set\n", name)
		os.Exit(2)
	}
	return v
}
