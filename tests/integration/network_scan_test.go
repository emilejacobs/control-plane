package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
)

// Issue #3 cycle: per-request row storage round-trips.
// CreateNetworkScanRequest → GetNetworkScan returns the pending shape;
// CompleteNetworkScan transitions it to done with the result payload;
// FailNetworkScan records the error code/message.
func TestRegistryNetworkScanLifecycle(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-netscan-01", "44444444-4444-4444-4444-aaaaaaaaaaaa")

	requestedAt := time.Date(2026, 5, 26, 14, 0, 0, 0, time.UTC)
	corrID := "corr-netscan-1"

	// Create: row exists with status=pending, result nil.
	if err := srv.Registry.CreateNetworkScanRequest(ctx, registry.NetworkScanRequest{
		CorrelationID: corrID,
		DeviceID:      deviceID,
		CIDR:          "192.168.1.0/24",
		RequestedAt:   requestedAt,
	}); err != nil {
		t.Fatalf("CreateNetworkScanRequest: %v", err)
	}

	got, err := srv.Registry.GetNetworkScan(ctx, corrID)
	if err != nil {
		t.Fatalf("GetNetworkScan: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status: got %q, want pending", got.Status)
	}
	if got.CIDR == nil || *got.CIDR != "192.168.1.0/24" {
		t.Errorf("cidr: got %v, want 192.168.1.0/24", got.CIDR)
	}
	if got.Result != nil {
		t.Errorf("result: got %+v, want nil", got.Result)
	}
	if got.ReturnedAt != nil {
		t.Errorf("returned_at: got %v, want nil", *got.ReturnedAt)
	}

	// Complete: transitions to done with the hosts list.
	returnedAt := requestedAt.Add(5 * time.Second)
	hosts := []networkscan.Host{
		{IP: "192.168.1.10", MAC: "44:19:b6:aa:bb:cc", Vendor: "Hikvision", OpenPorts: []int{80, 554}},
		{IP: "192.168.1.42", MAC: "3c:ef:8c:11:22:33", Vendor: "Dahua", OpenPorts: []int{443}},
	}
	if err := srv.Registry.CompleteNetworkScan(ctx, registry.NetworkScanCompletion{
		CorrelationID: corrID,
		Hosts:         hosts,
		ReturnedAt:    returnedAt,
	}); err != nil {
		t.Fatalf("CompleteNetworkScan: %v", err)
	}

	got, err = srv.Registry.GetNetworkScan(ctx, corrID)
	if err != nil {
		t.Fatalf("GetNetworkScan after complete: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("status: got %q, want done", got.Status)
	}
	if got.Result == nil || len(got.Result.Hosts) != 2 {
		t.Fatalf("result: got %+v", got.Result)
	}
	if got.Result.Hosts[0].Vendor != "Hikvision" || got.Result.Hosts[1].Vendor != "Dahua" {
		t.Errorf("hosts: got %+v", got.Result.Hosts)
	}
	if got.ReturnedAt == nil || !got.ReturnedAt.Equal(returnedAt) {
		t.Errorf("returned_at: got %v, want %v", got.ReturnedAt, returnedAt)
	}
}

// Failure path: FailNetworkScan records the agent's error code + message.
// Result stays nil; status is "error".
func TestRegistryNetworkScanFailurePath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-netscan-02", "44444444-4444-4444-4444-bbbbbbbbbbbb")

	corrID := "corr-netscan-fail"
	if err := srv.Registry.CreateNetworkScanRequest(ctx, registry.NetworkScanRequest{
		CorrelationID: corrID,
		DeviceID:      deviceID,
		CIDR:          "", // auto-detect
		RequestedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateNetworkScanRequest: %v", err)
	}

	failedAt := time.Now().UTC()
	if err := srv.Registry.FailNetworkScan(ctx, registry.NetworkScanFailure{
		CorrelationID: corrID,
		ErrorCode:     "network_scan.scan_failed",
		ErrorMessage:  "arp-scan: command not found",
		ReturnedAt:    failedAt,
	}); err != nil {
		t.Fatalf("FailNetworkScan: %v", err)
	}

	got, err := srv.Registry.GetNetworkScan(ctx, corrID)
	if err != nil {
		t.Fatalf("GetNetworkScan after fail: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("status: got %q, want error", got.Status)
	}
	if got.Result != nil {
		t.Errorf("result: got %+v, want nil on failure", got.Result)
	}
	if got.ErrorCode == nil || *got.ErrorCode != "network_scan.scan_failed" {
		t.Errorf("error_code: got %v", got.ErrorCode)
	}
	if got.CIDR != nil {
		t.Errorf("cidr: got %v, want nil for auto-detect", got.CIDR)
	}
}

// GetNetworkScan with an unknown correlation_id returns
// ErrNetworkScanNotFound so the API can return 404 cleanly.
func TestRegistryGetNetworkScanNotFound(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	_, err := srv.Registry.GetNetworkScan(ctx, "corr-does-not-exist")
	if !errors.Is(err, registry.ErrNetworkScanNotFound) {
		t.Errorf("expected ErrNetworkScanNotFound, got %v", err)
	}
}

// End-to-end: CmdResultIngester routes a network.scan success ACK to
// CompleteNetworkScan → the pending row transitions to status=done with
// the hosts list parsed out of the envelope's Result. This is the
// cycle that closes the loop the dashboard polls.
func TestCmdResultNetworkScanAckEndToEnd(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-netscan-e2e", "44444444-4444-4444-4444-eeeeeeeeeeee")

	requestedAt := time.Date(2026, 5, 26, 16, 0, 0, 0, time.UTC)
	corrID := "corr-netscan-e2e"
	if err := srv.Registry.CreateNetworkScanRequest(ctx, registry.NetworkScanRequest{
		CorrelationID: corrID,
		DeviceID:      deviceID,
		CIDR:          "192.168.1.0/24",
		RequestedAt:   requestedAt,
	}); err != nil {
		t.Fatalf("CreateNetworkScanRequest: %v", err)
	}

	returnedAt := requestedAt.Add(4 * time.Second)
	resultBytes, _ := json.Marshal(networkscan.Response{
		Hosts: []networkscan.Host{
			{IP: "192.168.1.10", MAC: "44:19:b6:aa:bb:cc", Vendor: "Hikvision", OpenPorts: []int{80, 554}},
		},
	})

	ing := ingest.NewCmdResultIngester(srv.Registry, func() time.Time { return returnedAt })
	msg := ingest.CmdResult{
		Result: envelope.Result{
			CorrelationID: corrID,
			CommandID:     "cmd-netscan-e2e",
			Type:          "network.scan",
			Success:       true,
			Result:        resultBytes,
		},
		DeviceID: deviceID,
	}
	if err := ing.Handle(ctx, msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got, err := srv.Registry.GetNetworkScan(ctx, corrID)
	if err != nil {
		t.Fatalf("GetNetworkScan after ACK: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("status: got %q, want done", got.Status)
	}
	if got.Result == nil || len(got.Result.Hosts) != 1 || got.Result.Hosts[0].Vendor != "Hikvision" {
		t.Errorf("hosts after ACK: got %+v", got.Result)
	}
	if got.ReturnedAt == nil || !got.ReturnedAt.Equal(returnedAt) {
		t.Errorf("ReturnedAt: got %v, want %v", got.ReturnedAt, returnedAt)
	}
}
