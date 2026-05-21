package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdempotencyStore persists the canonical response of state-mutating requests
// keyed by the client-supplied Idempotency-Key, so retries (per ADR-005 /
// ADR-012) return the same bytes without re-running the side effects.
type IdempotencyStore struct {
	pool *pgxpool.Pool
}

func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{pool: pool}
}

func (s *IdempotencyStore) Get(ctx context.Context, key string) (status int, body []byte, found bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT status_code, body FROM enrollment_idempotency WHERE key = $1`, key,
	).Scan(&status, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, fmt.Errorf("idempotency get: %w", err)
	}
	return status, body, true, nil
}

func (s *IdempotencyStore) Put(ctx context.Context, key string, status int, body []byte) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO enrollment_idempotency (key, status_code, body)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO NOTHING
	`, key, status, body)
	if err != nil {
		return fmt.Errorf("idempotency put: %w", err)
	}
	return nil
}
