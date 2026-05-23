package integration_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/auditmirror"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

// TestAuditMirrorExportDateWritesGzippedJSONLines is the Issue 28 tracer:
// an Exporter against a real Postgres + moto-backed S3 reads every
// audit_log row whose `at` falls within a UTC day and writes a gzipped
// JSON Lines object to s3://<bucket>/YYYY/MM/DD.jsonl.gz. The object's
// contents round-trip back to the original Entries through gunzip + JSON
// decode, one record per line, in `at`-ascending order.
func TestAuditMirrorExportDateWritesGzippedJSONLines(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	// Postgres + audit_log table.
	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	w := audit.NewPostgresWriter(pool)

	// Two rows on the export date, one on the next day. The exporter
	// should pick up only the two on-day rows.
	day := time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }
	mustInsert := func(at time.Time, action string, payload map[string]any) {
		t.Helper()
		actx := cplog.WithCorrelationID(ctx, "corr-"+action+"-"+at.Format("150405"))
		must(w.Write(actx, audit.Entry{
			Action: action, Outcome: "success", ActorType: audit.ActorOperator,
			Payload: payload,
		}))
		// audit_log defaults `at` to now(); overwrite it for the test.
		_, err := pool.Exec(ctx,
			`UPDATE audit_log SET at = $1 WHERE correlation_id = $2`,
			at, "corr-"+action+"-"+at.Format("150405"))
		must(err)
	}
	mustInsert(day.Add(10*time.Hour), "audit.test.early", map[string]any{"k": "v1"})
	mustInsert(day.Add(15*time.Hour), "audit.test.late", map[string]any{"k": "v2"})
	mustInsert(day.Add(48*time.Hour), "audit.test.next-day", map[string]any{"k": "out"})

	// moto-backed S3 with the bucket pre-created.
	s3Client := startMotoS3(t, ctx)
	const bucket = "uknomi-cp-audit-mirror-test"
	_, err := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	must(err)

	// Export the target day.
	exp := auditmirror.NewExporter(pool, s3Client, bucket)
	if err := exp.ExportDate(ctx, day); err != nil {
		t.Fatalf("ExportDate: %v", err)
	}

	// The key is partitioned YYYY/MM/DD.
	key := fmt.Sprintf("%04d/%02d/%02d.jsonl.gz", day.Year(), day.Month(), day.Day())
	out, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject %s: %v", key, err)
	}
	defer out.Body.Close()

	gz, err := gzip.NewReader(out.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	body, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("readall: %v", err)
	}

	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("decode line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	if len(lines) != 2 {
		t.Fatalf("export rows: got %d want 2 (next-day row should be excluded); body=%s", len(lines), body)
	}
	if lines[0]["action"] != "audit.test.early" || lines[1]["action"] != "audit.test.late" {
		t.Errorf("rows not ordered by `at` ascending; got %v / %v", lines[0]["action"], lines[1]["action"])
	}
	if lines[0]["correlation_id"] != "corr-audit.test.early-100000" {
		t.Errorf("correlation_id round-trip: got %v", lines[0]["correlation_id"])
	}
	if payload, ok := lines[0]["payload"].(map[string]any); !ok || payload["k"] != "v1" {
		t.Errorf("payload round-trip: got %v", lines[0]["payload"])
	}
}

// TestAuditMirrorExportDateIsIdempotent locks Issue 28 cycle 2: running
// the exporter twice for the same UTC day is a no-op on the second run.
// Object-lock on the production bucket would reject the second PutObject
// outright; the exporter short-circuits via HeadObject before that, so a
// re-run during a routine retry does not throw a confusing AccessDenied.
func TestAuditMirrorExportDateIsIdempotent(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	w := audit.NewPostgresWriter(pool)

	day := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)
	if err := w.Write(cplog.WithCorrelationID(ctx, "corr-idem"), audit.Entry{
		Action: "audit.idem.row", Outcome: "success", ActorType: audit.ActorOperator,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET at = $1 WHERE correlation_id = $2`, day.Add(time.Hour), "corr-idem"); err != nil {
		t.Fatal(err)
	}

	s3Client := startMotoS3(t, ctx)
	const bucket = "uknomi-cp-audit-mirror-idem"
	if _, err := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}

	exp := auditmirror.NewExporter(pool, s3Client, bucket)
	if err := exp.ExportDate(ctx, day); err != nil {
		t.Fatalf("first ExportDate: %v", err)
	}

	// Add another row for the same day AFTER the first export — re-running
	// must not include it, because the object already exists.
	if err := w.Write(cplog.WithCorrelationID(ctx, "corr-idem-late"), audit.Entry{
		Action: "audit.idem.late", Outcome: "success", ActorType: audit.ActorOperator,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET at = $1 WHERE correlation_id = $2`, day.Add(2*time.Hour), "corr-idem-late"); err != nil {
		t.Fatal(err)
	}

	if err := exp.ExportDate(ctx, day); err != nil {
		t.Fatalf("second ExportDate: %v", err)
	}

	key := fmt.Sprintf("%04d/%02d/%02d.jsonl.gz", day.Year(), day.Month(), day.Day())
	out, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer out.Body.Close()
	gz, err := gzip.NewReader(out.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	body, _ := io.ReadAll(gz)
	count := strings.Count(strings.TrimSpace(string(body)), "\n")
	// Two newlines → three lines OR one newline → two lines? The first
	// export wrote one line (1 entry). If idempotency works, the file
	// still has one line. If it doesn't, it'd have two.
	got := count + 1
	if strings.TrimSpace(string(body)) == "" {
		got = 0
	}
	if got != 1 {
		t.Errorf("rows in mirrored file: got %d want 1 (re-run must not overwrite); body=%s", got, body)
	}
}

// TestAuditMirrorExportRangeBackfill locks Issue 28 cycle 3: the
// backfill path writes one object per UTC day across the closed range
// [from, to]. The exporter binary's --from/--to flags drive this for
// the first-run case where audit_log has rows older than the daily job
// has been running.
func TestAuditMirrorExportRangeBackfill(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	w := audit.NewPostgresWriter(pool)

	// Three rows across three days; one day in the middle with no rows.
	dates := []time.Time{
		time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC),
		// Jan 2 deliberately empty.
		time.Date(2026, 1, 3, 5, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 4, 5, 0, 0, 0, time.UTC), // outside the range
	}
	for i, at := range dates {
		corr := fmt.Sprintf("corr-range-%d", i)
		if err := w.Write(cplog.WithCorrelationID(ctx, corr), audit.Entry{
			Action: "audit.range", Outcome: "success", ActorType: audit.ActorOperator,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE audit_log SET at = $1 WHERE correlation_id = $2`, at, corr); err != nil {
			t.Fatal(err)
		}
	}

	s3Client := startMotoS3(t, ctx)
	const bucket = "uknomi-cp-audit-mirror-range"
	if _, err := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}

	exp := auditmirror.NewExporter(pool, s3Client, bucket)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	if err := exp.ExportRange(ctx, from, to); err != nil {
		t.Fatalf("ExportRange: %v", err)
	}

	// Expect objects for Jan 1, Jan 2 (empty), Jan 3 — and NOT Jan 4.
	want := []struct {
		key      string
		nonEmpty bool
	}{
		{"2026/01/01.jsonl.gz", true},
		{"2026/01/02.jsonl.gz", false}, // empty gzip stream
		{"2026/01/03.jsonl.gz", true},
	}
	for _, w := range want {
		out, err := s3Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(w.key)})
		if err != nil {
			t.Errorf("GetObject %s: %v", w.key, err)
			continue
		}
		gz, err := gzip.NewReader(out.Body)
		if err != nil {
			out.Body.Close()
			t.Errorf("gzip %s: %v", w.key, err)
			continue
		}
		body, _ := io.ReadAll(gz)
		out.Body.Close()
		hasRows := strings.TrimSpace(string(body)) != ""
		if hasRows != w.nonEmpty {
			t.Errorf("%s nonEmpty: got %v want %v; body=%s", w.key, hasRows, w.nonEmpty, body)
		}
	}

	// Jan 4 was outside the range — no object should exist.
	if _, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("2026/01/04.jsonl.gz"),
	}); err == nil {
		t.Error("Jan 4 was outside the range but an object was written")
	}
}

// startMotoS3 stands up a moto container with the S3 service enabled.
// Moto supports S3 natively; per project_iot_mock_choice we use moto for
// non-IoT services too rather than spinning a second LocalStack.
func startMotoS3(t *testing.T, ctx context.Context) *s3.Client {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "motoserver/moto:latest",
		ExposedPorts: []string{"5000/tcp"},
		WaitingFor:   wait.ForListeningPort("5000/tcp").WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start moto: %v", err)
	}
	t.Cleanup(func() {
		timeout := 5 * time.Second
		_ = container.Stop(context.Background(), &timeout)
	})
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5000/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true // moto serves path-style by default
	})
}

// Silence unused-import warning in the bytes import; the helper uses
// bytes via gzip.NewReader's io.Reader contract.
var _ = bytes.NewBuffer
