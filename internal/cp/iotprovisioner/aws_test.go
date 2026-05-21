package iotprovisioner_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/emilejacobs/control-plane/internal/cp/iotprovisioner"
)

func TestAWSProvisionerCreatesThingAndCert(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	client := startMotoIoT(t, ctx)

	policyName := "uknomi-cp-device-policy"
	seedPolicy(t, ctx, client, policyName)

	p := iotprovisioner.NewAWS(client, policyName)
	cert, err := p.ProvisionDevice(ctx, "mac-mini-test-99")
	if err != nil {
		t.Fatalf("ProvisionDevice: %v", err)
	}

	if cert.ThingARN == "" {
		t.Errorf("ThingARN is empty")
	}
	if cert.CertARN == "" {
		t.Errorf("CertARN is empty")
	}
	if !strings.Contains(cert.CertPEM, "BEGIN CERTIFICATE") {
		t.Errorf("CertPEM is not a PEM cert; got %q", cert.CertPEM)
	}
	if !strings.Contains(cert.PrivKeyPEM, "PRIVATE KEY") {
		t.Errorf("PrivKeyPEM is not a PEM private key; got %q", cert.PrivKeyPEM)
	}
	if !cert.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt should be in the future; got %v", cert.ExpiresAt)
	}

	// LocalStack should now know about the thing.
	if _, err := client.DescribeThing(ctx, &iot.DescribeThingInput{
		ThingName: aws.String("mac-mini-test-99"),
	}); err != nil {
		t.Errorf("DescribeThing after provision: %v", err)
	}

	// The cert should have our policy attached — this is what makes the
	// returned cert actually able to connect + publish.
	attached, err := client.ListAttachedPolicies(ctx, &iot.ListAttachedPoliciesInput{
		Target: aws.String(cert.CertARN),
	})
	if err != nil {
		t.Fatalf("ListAttachedPolicies: %v", err)
	}
	found := false
	for _, pol := range attached.Policies {
		if aws.ToString(pol.PolicyName) == policyName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("policy %q not attached to cert %s", policyName, cert.CertARN)
	}
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

// startMotoIoT starts a moto-server container and returns an AWS IoT client
// pointed at it. LocalStack Community does not implement IoT (Pro-only); moto
// is the supported open-source alternative named in ADR-012 and Issue 03.
func startMotoIoT(t *testing.T, ctx context.Context) *iot.Client {
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
	client := iot.NewFromConfig(cfg, func(o *iot.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	return client
}

func seedPolicy(t *testing.T, ctx context.Context, c *iot.Client, name string) {
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
