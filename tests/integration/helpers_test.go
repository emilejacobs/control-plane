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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
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
	t.Helper()
	if authnCfg.SigningKey == nil {
		authnCfg.SigningKey = testSigningKey
	}
	if authnCfg.TotpEncryptionKey == nil {
		authnCfg.TotpEncryptionKey = testTotpKey
	}
	pool := startPostgres(t, ctx)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	iot := iotprovisioner.NewFake()
	reg := registry.New(pool, iot, registry.Config{BootstrapKey: testBootstrapKey})
	idemStore := storage.NewIdempotencyStore(pool)
	authnSvc := authn.New(pool, authnCfg)
	logs := &syncBuffer{}
	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Registry:         reg,
		AuthN:            authnSvc,
		IdempotencyStore: idemStore,
		Logger:           cplog.New(logs, "cp-api-test"),
	}))
	t.Cleanup(srv.Close)
	return &testServer{URL: srv.URL, Pool: pool, IoT: iot, Logs: logs, AuthN: authnSvc, Registry: reg}
}

// mintAccessToken returns a freshly-signed operator access token valid for a
// server built by newTestServer — the bearer token its Auth middleware
// expects. It signs with testSigningKey directly, so no DB operator is
// needed; the middleware only verifies the JWT.
func mintAccessToken(t *testing.T) string {
	t.Helper()
	signer := authn.NewSigner(testSigningKey, time.Hour)
	token, err := signer.Issue(authn.TokenClaims{
		OperatorID: "00000000-0000-0000-0000-0000000000aa",
		Email:      "operator@acmecorp.test",
		IsStaff:    true,
	})
	if err != nil {
		t.Fatalf("mint access token: %v", err)
	}
	return token
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

func startPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
