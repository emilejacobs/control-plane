// Package auditmirror copies audit_log rows to S3 as gzipped JSON Lines.
// One object per UTC day at s3://<bucket>/YYYY/MM/DD.jsonl.gz; one row
// per line, ordered by `at` ascending. ADR-023 + Issue 28.
package auditmirror

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

// S3PutObjectAPI is the subset of *s3.Client the Exporter uses. The
// interface keeps tests free of the full SDK surface; production wires
// *s3.Client which satisfies it.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// Row is the on-disk shape of one audit_log entry in the mirrored file.
// The field order is stable across versions so a downstream SIEM ingestor
// can rely on it.
type Row struct {
	ID            string          `json:"id"`
	At            time.Time       `json:"at"`
	Action        string          `json:"action"`
	ActorID       string          `json:"actor_id"`
	ActorType     string          `json:"actor_type"`
	ResourceKind  string          `json:"resource_kind"`
	ResourceID    string          `json:"resource_id"`
	CorrelationID string          `json:"correlation_id"`
	SourceIP      string          `json:"source_ip"`
	UserAgent     string          `json:"user_agent"`
	Outcome       string          `json:"outcome"`
	Payload       json.RawMessage `json:"payload"`
}

// Exporter writes one S3 object per UTC day from audit_log rows.
type Exporter struct {
	pool   *pgxpool.Pool
	s3     S3PutObjectAPI
	bucket string
}

// NewExporter binds a pool, S3 client, and bucket name.
func NewExporter(pool *pgxpool.Pool, s3 S3PutObjectAPI, bucket string) *Exporter {
	return &Exporter{pool: pool, s3: s3, bucket: bucket}
}

// ExportDate writes every audit_log row whose `at` falls within the UTC
// day covering `day` (`at >= floor(day)` and `at < floor(day)+24h`) as
// a gzipped JSON Lines object to `<bucket>/YYYY/MM/DD.jsonl.gz`. A day
// with zero rows still writes an empty (header-only) gzip stream so the
// downstream consumer sees an explicit "we ran" signal.
//
// The full day's rows are buffered in memory before the PutObject call.
// Phase 1 row volume is ~3000 rows/day (every state-mutating request +
// every IoT lifecycle event); a few hundred KB compressed. Streaming via
// io.Pipe is the obvious shape but it races against the SDK's body
// buffering and pgx's connection lifecycle — the buffered shape is the
// disciplined trade.
func (e *Exporter) ExportDate(ctx context.Context, day time.Time) error {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	key := fmt.Sprintf("%04d/%02d/%02d.jsonl.gz", start.Year(), start.Month(), start.Day())

	// Idempotency short-circuit: skip the day if its object already exists.
	// The production bucket runs in governance-mode object-lock (1y), so a
	// second PutObject to the same key would otherwise fail AccessDenied
	// halfway through the run; this avoids the spurious-error noise. An
	// operator who deliberately wants to re-export deletes the object first
	// (governance mode permits the bypass-IAM path; runbook covers it).
	if _, err := e.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(e.bucket),
		Key:    aws.String(key),
	}); err == nil {
		return nil
	} else {
		var notFound *types.NotFound
		var noSuchKey *types.NoSuchKey
		if !errors.As(err, &notFound) && !errors.As(err, &noSuchKey) {
			return fmt.Errorf("head %s: %w", key, err)
		}
	}

	rows, err := e.pool.Query(ctx, `
		SELECT id::text, at, action, actor_id, actor_type, resource_kind, resource_id,
		       correlation_id, source_ip, user_agent, outcome, payload
		FROM audit_log
		WHERE at >= $1 AND at < $2
		ORDER BY at ASC, id ASC
	`, start, end)
	if err != nil {
		return fmt.Errorf("query audit_log: %w", err)
	}
	defer rows.Close()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.At, &r.Action, &r.ActorID, &r.ActorType,
			&r.ResourceKind, &r.ResourceID, &r.CorrelationID, &r.SourceIP,
			&r.UserAgent, &r.Outcome, &r.Payload); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if err := enc.Encode(&r); err != nil {
			return fmt.Errorf("encode: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}

	if _, err := e.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:          aws.String(e.bucket),
		Key:             aws.String(key),
		Body:            bytes.NewReader(buf.Bytes()),
		ContentEncoding: aws.String("gzip"),
		ContentType:     aws.String("application/x-ndjson"),
	}); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

// ExportRange exports every UTC day in the closed range [from, to]. Both
// endpoints are floored to their UTC dates. A day with zero audit_log
// rows still gets an empty object so the downstream sees an explicit
// "we ran" signal for every covered day.
//
// The first error halts the range — partial output stays on S3, and a
// subsequent re-run picks up where the previous failed (idempotent
// ExportDate short-circuits the days that already exist).
func (e *Exporter) ExportRange(ctx context.Context, from, to time.Time) error {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if err := e.ExportDate(ctx, d); err != nil {
			return fmt.Errorf("ExportDate %s: %w", d.Format("2006-01-02"), err)
		}
	}
	return nil
}
