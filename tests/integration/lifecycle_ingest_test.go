package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/presence"
	"github.com/emilejacobs/control-plane/internal/cp/sqsconsumer"
)

// TestLifecycleIngestEndToEnd is Issue 08 cycle 6: an IoT lifecycle event
// published to SQS flows through SQSConsumer + LifecycleIngester and flips
// devices.is_online — within ~5s, visible in the API (AC 27, 30). An event
// for an unknown device lands in the DLQ.
func TestLifecycleIngestEndToEnd(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-lifecycle-01", "88888888-8888-8888-8888-555555555555")
	token := mintAccessToken(t)

	// Bring the device online first, so a disconnect has something to flip.
	if err := srv.Registry.SetPresence(ctx, deviceID, true, time.Now().UTC()); err != nil {
		t.Fatalf("seed online: %v", err)
	}

	sqsClient := startMotoSQS(t, ctx)
	mainURL := createQueue(t, ctx, sqsClient, "cp-presence-lifecycle")
	dlqURL := createQueue(t, ctx, sqsClient, "cp-presence-lifecycle-dlq")

	logs := &syncBuffer{}
	ingester := ingest.NewLifecycleIngester(presence.New(), srv.Registry, nil)
	consumer := sqsconsumer.NewConsumer[ingest.Lifecycle](sqsClient, ingester.Handle, sqsconsumer.Config{
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

	// is_online as the API reports it.
	apiOnline := func() bool {
		resp := doDeviceGet(t, srv.URL, deviceID, token)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET device: got %d; body=%s", resp.StatusCode, raw)
		}
		var out struct {
			IsOnline bool `json:"is_online"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out.IsOnline
	}
	waitOnline := func(want bool, desc string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if apiOnline() == want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("%s: API still reports is_online=%v after 5s", desc, !want)
	}

	// AC 27/30: a disconnected event flips the device offline within ~5s.
	sendMessage(t, ctx, sqsClient, mainURL,
		fmt.Sprintf(`{"clientId":%q,"eventType":"disconnected","correlation_id":"corr-lc-1"}`, deviceID))
	waitOnline(false, "after disconnected event")

	// A connected event brings it back online.
	sendMessage(t, ctx, sqsClient, mainURL,
		fmt.Sprintf(`{"clientId":%q,"eventType":"connected","correlation_id":"corr-lc-2"}`, deviceID))
	waitOnline(true, "after connected event")

	// A lifecycle event for an unknown device is poison — it lands in the DLQ.
	sendMessage(t, ctx, sqsClient, mainURL,
		`{"clientId":"00000000-0000-0000-0000-000000000000","eventType":"disconnected","correlation_id":"corr-lc-bad"}`)
	deadline := time.Now().Add(10 * time.Second)
	var dlqDepth int
	for time.Now().Before(deadline) {
		dlqDepth = queueDepth(t, ctx, sqsClient, dlqURL)
		if dlqDepth >= 1 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if dlqDepth < 1 {
		t.Errorf("DLQ depth: got %d want >= 1 (unknown-device lifecycle event)", dlqDepth)
	}

	// The consumer survived the poison message.
	select {
	case err := <-consumerDone:
		t.Fatalf("consumer exited early: %v", err)
	default:
	}
}
