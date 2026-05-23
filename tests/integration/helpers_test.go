package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/authz"
	"github.com/emilejacobs/control-plane/internal/cp/bootstrap"
	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

const testBootstrapKey = "test-bootstrap-key"

// testSigningKey is the HS256 secret used in integration tests. Real
// deployments load this from JWT_SIGNING_KEY (base64-encoded 32+ bytes).
var testSigningKey = []byte("integration-test-signing-key-zzzz-32-bytes")

// testTotpKey is the fixed 32-byte AES-256 key for TOTP-secret encryption in
// integration tests. Real deployments load this from TOTP_ENCRYPTION_KEY.
var testTotpKey = []byte("uknomi-cp-integration-totp-key!!")

// testServer bundles the live fixtures an integration test needs: an HTTP
// server wired to a Registry + fake IoTProvisioner, plus the underlying
// Postgres pool for direct row assertions and a captured log buffer for
// correlation-id tests. Tests that don't care about logs ignore Logs.
type testServer struct {
	URL      string
	Pool     *pgxpool.Pool
	IoT      *iotprovisioner.Fake
	Logs     *syncBuffer
	AuthN    *authn.AuthN
	AuthZ    *authz.AuthZ
	Registry *registry.Registry
}

// syncBuffer is bytes.Buffer with a mutex for the concurrent-writer case
// (slog may emit from middleware goroutines). Sequential test flows don't
// need it, but it's cheap insurance.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// newTestServer starts a Postgres testcontainer, applies migrations, wires the
// CP API router with feature flags enabled, and registers t.Cleanup hooks.
func newTestServer(t *testing.T, ctx context.Context) *testServer {
	return newTestServerCfg(t, ctx, authn.Config{SigningKey: testSigningKey})
}

// newTestServerCfg is newTestServer with an explicit AuthN config — the
// lockout test uses it to inject a fake clock. An empty SigningKey is
// backfilled with testSigningKey so callers only set what they care about.
func newTestServerCfg(t *testing.T, ctx context.Context, authnCfg authn.Config) *testServer {
	return buildTestServer(t, ctx, startPostgres(t, ctx, nil), authnCfg)
}

// newTracedTestServer is newTestServer with a pgx QueryTracer attached to the
// pool, returning the recorder so the CI-gate test can inspect every SQL
// statement the handlers ran.
func newTracedTestServer(t *testing.T, ctx context.Context) (*testServer, *queryRecorder) {
	rec := &queryRecorder{}
	return buildTestServer(t, ctx, startPostgres(t, ctx, rec), authn.Config{}), rec
}

// testBootstrapVerifier builds a bootstrap-key verifier that accepts
// testBootstrapKey — the integration-test equivalent of the Secrets
// Manager-backed verifier production wires in.
func testBootstrapVerifier(t *testing.T, ctx context.Context) *bootstrap.Verifier {
	t.Helper()
	v, err := bootstrap.NewVerifier(ctx, bootstrap.FixedKey(testBootstrapKey))
	if err != nil {
		t.Fatalf("bootstrap verifier: %v", err)
	}
	return v
}

// buildTestServer migrates the pool, wires the CP API router, and registers
// cleanup. An empty SigningKey / TotpEncryptionKey is backfilled with the
// test keys.
func buildTestServer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, authnCfg authn.Config) *testServer {
	t.Helper()
	if authnCfg.SigningKey == nil {
		authnCfg.SigningKey = testSigningKey
	}
	if authnCfg.TotpEncryptionKey == nil {
		authnCfg.TotpEncryptionKey = testTotpKey
	}
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	iot := iotprovisioner.NewFake()
	reg := registry.New(pool, iot, registry.Config{BootstrapVerifier: testBootstrapVerifier(t, ctx)})
	idemStore := storage.NewIdempotencyStore(pool)
	authnSvc := authn.New(pool, authnCfg)
	authzSvc := authz.New(pool)
	auditW := audit.NewPostgresWriter(pool)
	logs := &syncBuffer{}
	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Registry:         reg,
		AuthN:            authnSvc,
		AuthZ:            authzSvc,
		IdempotencyStore: idemStore,
		Audit:            auditW,
		Logger:           cplog.New(logs, "cp-api-test"),
	}))
	t.Cleanup(srv.Close)
	return &testServer{URL: srv.URL, Pool: pool, IoT: iot, Logs: logs, AuthN: authnSvc, AuthZ: authzSvc, Registry: reg}
}

// queryRecorder is a pgx.QueryTracer that records every SQL statement run on
// the pool — the runtime hook behind the scopedDeviceQuery CI gate.
type queryRecorder struct {
	mu  sync.Mutex
	sql []string
}

func (r *queryRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	r.sql = append(r.sql, data.SQL)
	r.mu.Unlock()
	return ctx
}

func (r *queryRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// reset clears recorded statements; snapshot returns a copy of them.
func (r *queryRecorder) reset() {
	r.mu.Lock()
	r.sql = nil
	r.mu.Unlock()
}

func (r *queryRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sql...)
}

// mintAccessToken inserts a TOTP-enrolled staff operator and returns a signed
// access token for it. Gated read endpoints sit behind Auth +
// RequireTotpEnrolled, so the backing operator must exist and be enrolled for
// the request to reach the handler.
func mintAccessToken(t *testing.T, ctx context.Context, srv *testServer) string {
	t.Helper()
	const operatorID = "00000000-0000-0000-0000-0000000000aa"
	const email = "gated-reader@acmecorp.test"
	if _, err := srv.Pool.Exec(ctx, `
		INSERT INTO operators (id, email, password_hash, is_staff, totp_secret_encrypted)
		VALUES ($1, $2, 'unused-hash', true, $3)
		ON CONFLICT (id) DO NOTHING
	`, operatorID, email, []byte("totp-secret-ciphertext")); err != nil {
		t.Fatalf("insert enrolled operator: %v", err)
	}
	signer := authn.NewSigner(testSigningKey, time.Hour)
	token, err := signer.Issue(authn.TokenClaims{
		OperatorID: operatorID,
		Email:      email,
		IsStaff:    true,
	})
	if err != nil {
		t.Fatalf("mint access token: %v", err)
	}
	return token
}

// staffCtx returns ctx carrying a staff (full-fleet) authz scope. Tests that
// call registry.GetByID directly — as a staff observer checking device state
// — need it, since device reads are site-scoped and fail closed without one.
func staffCtx(ctx context.Context) context.Context {
	return authz.ContextWithScope(ctx, authz.SiteFilter{All: true})
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not in PATH; skipping integration test")
	}
	cmd := exec.Command("docker", "info")
	cmd.Env = append(os.Environ(), "DOCKER_CLI_HINTS=false")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Run(); err != nil {
		t.Skip("docker daemon not reachable; skipping integration test")
	}
}

// startPostgres starts a Postgres testcontainer and returns a pool. A non-nil
// tracer is attached to every connection — the CI-gate test passes one.
func startPostgres(t *testing.T, ctx context.Context, tracer pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "cp",
			"POSTGRES_PASSWORD": "cp",
			"POSTGRES_DB":       "cp",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		timeout := 5 * time.Second
		_ = container.Stop(context.Background(), &timeout)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	dsn := fmt.Sprintf("postgres://cp:cp@%s:%s/cp?sslmode=disable", host, port.Port())
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgxpool.ParseConfig: %v", err)
	}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
