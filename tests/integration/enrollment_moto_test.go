package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/storage"
)

// TestEnrollmentAgainstMotoIoT exercises the full POST /enrollments path
// with the AWS-SDK-backed Provisioner pointed at moto. It's the Issue 03
// acceptance criterion "integration test exercises the full flow against
// a Postgres test container and a LocalStack or moto IoT endpoint."
func TestEnrollmentAgainstMotoIoT(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pool := startPostgres(t, ctx, nil)
	if err := storage.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	iotClient := startMotoIoTForIntegration(t, ctx)
	const policyName = "uknomi-cp-device-policy"
	seedIoTPolicy(t, ctx, iotClient, policyName)

	prov := iotprovisioner.NewAWS(iotClient, policyName)
	reg := registry.New(pool, prov, registry.Config{BootstrapVerifier: testBootstrapVerifier(t, ctx)})
	store := storage.NewIdempotencyStore(pool)
	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Registry:         reg,
		IdempotencyStore: store,
	}))
	t.Cleanup(srv.Close)

	const hwUUID = "99999999-9999-9999-9999-999999999999"
	const hostname = "mac-mini-acme-99"
	body, err := json.Marshal(map[string]any{
		"bootstrap_key": testBootstrapKey,
		"hostname":      hostname,
		"hardware_uuid": hwUUID,
		"hardware_kind": "mac",
		"os_version":    "macOS 15.0",
		"agent_version": "0.1.0",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/enrollments", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", hwUUID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 201; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		DeviceID          string `json:"device_id"`
		MtlsCertPEM       string `json:"mtls_cert_pem"`
		MtlsPrivateKeyPEM string `json:"mtls_private_key_pem"`
		IoTThingARN       string `json:"iot_thing_arn"`
		MtlsCertExpiresAt string `json:"mtls_cert_expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(out.MtlsCertPEM, "BEGIN CERTIFICATE") {
		t.Errorf("response cert PEM doesn't look like a cert: %q", out.MtlsCertPEM)
	}
	if !strings.Contains(out.MtlsPrivateKeyPEM, "PRIVATE KEY") {
		t.Errorf("response private key PEM doesn't look like a key")
	}
	if out.IoTThingARN == "" {
		t.Errorf("iot_thing_arn empty in response")
	}
	expiresAt, err := time.Parse(time.RFC3339, out.MtlsCertExpiresAt)
	if err != nil {
		t.Errorf("mtls_cert_expires_at not RFC3339: %v", err)
	} else if !expiresAt.After(time.Now()) {
		t.Errorf("mtls_cert_expires_at is not in the future: %v", expiresAt)
	}

	// Moto should now actually have the thing.
	thing, err := iotClient.DescribeThing(ctx, &iot.DescribeThingInput{
		ThingName: aws.String(out.DeviceID),
	})
	if err != nil {
		t.Fatalf("DescribeThing in moto: %v", err)
	}
	if aws.ToString(thing.ThingName) != out.DeviceID {
		t.Errorf("moto thing name: got %q want %q", aws.ToString(thing.ThingName), out.DeviceID)
	}

	// And the row should be persisted with the same hardware_uuid the install
	// script sent — the dedupe key for future retries.
	var dbHostname string
	if err := pool.QueryRow(ctx,
		`SELECT hostname FROM devices WHERE hardware_uuid = $1`, hwUUID,
	).Scan(&dbHostname); err != nil {
		t.Fatalf("query devices: %v", err)
	}
	if dbHostname != hostname {
		t.Errorf("devices row hostname: got %q want %q", dbHostname, hostname)
	}
}

// startMotoIoTForIntegration mirrors the helper in
// internal/cp/iotprovisioner/aws_test.go. Test files in different packages
// can't share helpers; duplicating ~30 lines is cheaper than extracting a
// shared non-_test helper package right now. Revisit if a third moto-using
// integration test lands.
func startMotoIoTForIntegration(t *testing.T, ctx context.Context) *iot.Client {
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
	return iot.NewFromConfig(cfg, func(o *iot.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func seedIoTPolicy(t *testing.T, ctx context.Context, c *iot.Client, name string) {
	t.Helper()
	doc := `{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Action": ["iot:Connect", "iot:Publish", "iot:Subscribe", "iot:Receive"],
			"Resource": "*"
		}]
	}`
	if _, err := c.CreatePolicy(ctx, &iot.CreatePolicyInput{
		PolicyName:     aws.String(name),
		PolicyDocument: aws.String(doc),
	}); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
}
