package integration_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
)

// TestHeartbeatIngestEndToEnd is Issue 07 cycle 8: a heartbeat published to
// the SQS queue flows through SQSConsumer + PresenceIngester to
// devices.last_seen within 5 seconds, and malformed heartbeats land in the
// DLQ — with an audit log line — rather than crashing the consumer.
func TestHeartbeatIngestEndToEnd(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	// Postgres + registry + an enrolled device.
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-ingest-01", "55555555-5555-5555-5555-555555555555")

	// moto-backed SQS: a main queue and a dead-letter queue.
	sqsClient := startMotoSQS(t, ctx)
	mainURL := createQueue(t, ctx, sqsClient, "cp-presence-heartbeats")
	dlqURL := createQueue(t, ctx, sqsClient, "cp-presence-heartbeats-dlq")

	// Wire the consumer the way cmd/cp-ingest will.
	logs := &syncBuffer{}
	p := presence.New()
	ingester := ingest.NewPresenceIngester(p, srv.Registry, nil)
	consumer := sqsconsumer.NewConsumer[ingest.Heartbeat](sqsClient, ingester.Handle, sqsconsumer.Config{
		QueueURL:    mainURL,
		DLQURL:      dlqURL,
		WaitSeconds: 1,
		Logger:      cplog.New(logs, "cp-ingest-test"),
	})
	runCtx, cancel := context.WithCancel(ctx)
	consumerDone := make(chan error, 1)
	go func() { consumerDone <- consumer.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		<-consumerDone
	})

	// A heartbeat in the post-IoT-Rule shape: device_id from topic(2),
	// correlation_id from the agent's telemetry envelope.
	sendMessage(t, ctx, sqsClient, mainURL,
		fmt.Sprintf(`{"device_id":%q,"correlation_id":"corr-e2e-1"}`, deviceID))

	// AC1: devices.last_seen is updated within 5 seconds.
	deadline := time.Now().Add(5 * time.Second)
	var lastSeen *time.Time
	for time.Now().Before(deadline) {
		dev, err := srv.Registry.GetByID(staffCtx(ctx), deviceID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if dev.LastSeen != nil {
			lastSeen = dev.LastSeen
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastSeen == nil {
		t.Fatal("devices.last_seen was not updated within 5s of the heartbeat")
	}
	if time.Since(*lastSeen) > 30*time.Second {
		t.Errorf("last_seen is not recent: %v", *lastSeen)
	}

	// AC3: malformed heartbeats land in the DLQ — unparseable JSON
	// (rejected by the consumer) and an unknown device (rejected as poison
	// by the ingester).
	sendMessage(t, ctx, sqsClient, mainURL, `{this is not json`)
	sendMessage(t, ctx, sqsClient, mainURL,
		`{"device_id":"00000000-0000-0000-0000-000000000000","correlation_id":"corr-e2e-bad"}`)

	deadline = time.Now().Add(10 * time.Second)
	var dlqDepth int
	for time.Now().Before(deadline) {
		dlqDepth = queueDepth(t, ctx, sqsClient, dlqURL)
		if dlqDepth >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if dlqDepth < 2 {
		t.Errorf("DLQ depth: got %d want >= 2 (unparseable + unknown-device)", dlqDepth)
	}

	// The consumer survived the poison messages.
	select {
	case err := <-consumerDone:
		t.Fatalf("consumer exited early: %v", err)
	default:
	}

	// Each rejection is audit-logged.
	if n := strings.Count(logs.String(), "audit.message_rejected"); n < 2 {
		t.Errorf("audit.message_rejected log lines: got %d want >= 2\nlog:\n%s", n, logs.String())
	}
}

// startMotoSQS starts a moto container and returns an SQS client pointed at
// it. Mirrors startMotoIoTForIntegration; see that helper's note on why the
// ~30 lines are duplicated rather than extracted.
func startMotoSQS(t *testing.T, ctx context.Context) *sqs.Client {
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
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func createQueue(t *testing.T, ctx context.Context, c *sqs.Client, name string) string {
	t.Helper()
	out, err := c.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("CreateQueue %s: %v", name, err)
	}
	return aws.ToString(out.QueueUrl)
}

func sendMessage(t *testing.T, ctx context.Context, c *sqs.Client, queueURL, body string) {
	t.Helper()
	if _, err := c.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(body),
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
}

func queueDepth(t *testing.T, ctx context.Context, c *sqs.Client, queueURL string) int {
	t.Helper()
	out, err := c.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages},
	})
	if err != nil {
		t.Fatalf("GetQueueAttributes: %v", err)
	}
	n, _ := strconv.Atoi(out.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessages)])
	return n
}
