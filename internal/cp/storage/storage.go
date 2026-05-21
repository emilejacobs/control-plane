// Package storage owns Postgres connection management and schema migrations.
//
// Per ADR-019, migrations are SQL files embedded into the deployed binary and
// run on startup via goose. Multi-instance serialization (Postgres advisory
// lock) is not yet wired — single-instance test usage today; the lock lands
// when the multi-Fargate-task case actually exists.
package storage

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate runs every pending migration from the embedded migrations directory
// against the Postgres pool's database. Safe to call on every process start.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	cfg := pool.Config().ConnConfig
	db := stdlib.OpenDB(*cfg)
	defer db.Close()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
