package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
	"github.com/emilejacobs/control-plane/internal/protocol/healthprobes"
)

// TestHealthProbeIngestEndToEnd — Phase 2 fleet-health-probes (#19): a
// report published in the post-IoT-Rule shape flows SQS → consumer →
// ingester → device_health_probes, and a malformed / unknown-device
// report lands in the DLQ without killing the consumer.
func TestHealthProbeIngestEndToEnd(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-probes-e2e", "33333333-4444-5555-6666-777777777777")

	sqsClient := startMotoSQS(t, ctx)
	mainURL := createQueue(t, ctx, sqsClient, "cp-health-probes")
	dlqURL := createQueue(t, ctx, sqsClient, "cp-health-probes-dlq")

	logs := &syncBuffer{}
	ingester := ingest.NewHealthProbeIngester(srv.Registry, nil)
	ingester.Logger = cplog.New(logs, "cp-ingest-test")
	consumer := sqsconsumer.NewConsumer[healthprobes.Report](sqsClient, ingester.Handle, sqsconsumer.Config{
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

	report := healthprobes.Report{
		DeviceID:      deviceID,
		CorrelationID: "corr-probes-e2e",
		ReportedAt:    time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		Probes: []healthprobes.Result{
			{Name: healthprobes.ProbeAutoLogin, Status: healthprobes.StatusGreen, State: "configured"},
			{Name: healthprobes.ProbeUSBAudio, Status: healthprobes.StatusRed, State: "missing"},
		},
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	sendMessage(t, ctx, sqsClient, mainURL, string(body))

	// The two probe rows are upserted within 5 seconds.
	deadline := time.Now().Add(5 * time.Second)
	var rowCount int
	for time.Now().Before(deadline) {
		_ = srv.Pool.QueryRow(ctx,
			`SELECT count(*) FROM device_health_probes WHERE device_id = $1`, deviceID).Scan(&rowCount)
		if rowCount >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if rowCount < 2 {
		t.Fatalf("device_health_probes rows: got %d want 2 within 5s", rowCount)
	}

	// Malformed JSON + a report for an unknown device both land in the DLQ.
	sendMessage(t, ctx, sqsClient, mainURL, `{not json`)
	sendMessage(t, ctx, sqsClient, mainURL,
		fmt.Sprintf(`{"device_id":"00000000-0000-0000-0000-000000000000","correlation_id":"corr-bad","probes":[{"name":%q,"status":"red","state":"missing"}]}`,
			healthprobes.ProbeAutoLogin))

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
		t.Errorf("DLQ depth: got %d want >= 2 (malformed + unknown-device)", dlqDepth)
	}

	select {
	case err := <-consumerDone:
		t.Fatalf("consumer exited early: %v", err)
	default:
	}
}
