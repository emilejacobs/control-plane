// Command taxonomy-sync mirrors the upstream clients/sites HTTP API
// into CP's Postgres (ADR-033). One-shot Fargate task; EventBridge
// fires it at 00:05 UTC daily, and cp-api's POST /taxonomy/sync can
// trigger an ad-hoc run via ECS RunTask. Issue #18.
//
// Required env:
//
//	DB_DSN                   Postgres DSN (postgres://...)
//	TAXONOMY_API_BASE_URL    e.g. https://api.uknomi.com
//	TAXONOMY_USERNAME        Cognito service-account username
//	TAXONOMY_PASSWORD        Cognito service-account password
//
// Flag:
//
//	--dry-run  Exercise auth + parsing without writing to Postgres.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
)

func main() {
	logger := cplog.New(os.Stdout, "taxonomy-sync")
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		// Match the audit-mirror failure pattern so the CloudWatch
		// log-metric filter "taxonomy-sync failed" matches and the
		// alarm fires (ADR-023 § Observability).
		logger.Error("taxonomy-sync failed", "err", err)
		os.Exit(1)
	}
	logger.Info("taxonomy-sync completed")
}

func run(logger *slog.Logger) error {
	dryRun := flag.Bool("dry-run", false, "Exercise auth + parsing without writing to Postgres.")
	flag.Parse()

	dsn := mustEnv("DB_DSN")
	apiURL := mustEnv("TAXONOMY_API_BASE_URL")
	username := mustEnv("TAXONOMY_USERNAME")
	password := mustEnv("TAXONOMY_PASSWORD")

	// Marshal once just to keep the credential-presence log line tidy
	// (no secrets, only shape).
	logger.Info("starting", "dry_run", *dryRun, "api", apiURL,
		"creds_shape", credShape(username, password))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	runner := taxonomy.NewRunner(
		taxonomy.NewClient(apiURL, username, password),
		taxonomy.NewStore(pool),
		time.Now,
	)
	if *dryRun {
		return runner.RunDryRun(ctx)
	}
	return runner.Run(ctx)
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env var %s is not set\n", name)
		os.Exit(2)
	}
	return v
}

// credShape returns a JSON description of which credential fields
// are present (no values). Lets the structured log confirm Secrets
// Manager wired both fields without leaking either.
func credShape(username, password string) string {
	b, _ := json.Marshal(map[string]bool{
		"username": username != "",
		"password": password != "",
	})
	return string(b)
}
