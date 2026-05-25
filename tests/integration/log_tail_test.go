package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// Phase 2 slice 3 cycle 1: per-request row storage round-trips.
// CreateLogTailRequest → GetLogTail returns the pending shape;
// CompleteLogTail transitions it to done with content + truncation
// metadata; FailLogTail records the error code/message.
func TestRegistryLogTailLifecycle(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-logtail-01", "33333333-3333-3333-3333-aaaaaaaaaaaa")

	requestedAt := time.Date(2026, 5, 24, 22, 0, 0, 0, time.UTC)
	corrID := "corr-logtail-1"

	// Create: row exists with status=pending, content nil.
	if err := srv.Registry.CreateLogTailRequest(ctx, registry.LogTailRequest{
		CorrelationID:  corrID,
		DeviceID:       deviceID,
		LogName:        "agent",
		LinesRequested: 200,
		RequestedAt:    requestedAt,
	}); err != nil {
		t.Fatalf("CreateLogTailRequest: %v", err)
	}

	got, err := srv.Registry.GetLogTail(ctx, corrID)
	if err != nil {
		t.Fatalf("GetLogTail: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status: got %q, want pending", got.Status)
	}
	if got.LogName != "agent" || got.LinesRequested != 200 {
		t.Errorf("request fields: got %+v", got)
	}
	if got.Content != nil {
		t.Errorf("content: got %v, want nil", *got.Content)
	}
	if got.ReturnedAt != nil {
		t.Errorf("returned_at: got %v, want nil", *got.ReturnedAt)
	}

	// Complete: transitions to done with content + truncation metadata.
	returnedAt := requestedAt.Add(2 * time.Second)
	if err := srv.Registry.CompleteLogTail(ctx, registry.LogTailCompletion{
		CorrelationID: corrID,
		Content:       "line 1\nline 2\nline 3\n",
		Truncated:     true,
		TruncatedFrom: 500,
		ReturnedAt:    returnedAt,
	}); err != nil {
		t.Fatalf("CompleteLogTail: %v", err)
	}

	got, err = srv.Registry.GetLogTail(ctx, corrID)
	if err != nil {
		t.Fatalf("GetLogTail after complete: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("status: got %q, want done", got.Status)
	}
	if got.Content == nil || *got.Content != "line 1\nline 2\nline 3\n" {
		t.Errorf("content: got %v", got.Content)
	}
	if !got.Truncated {
		t.Error("truncated: got false, want true")
	}
	if got.TruncatedFrom == nil || *got.TruncatedFrom != 500 {
		t.Errorf("truncated_from: got %v, want 500", got.TruncatedFrom)
	}
	if got.ReturnedAt == nil || !got.ReturnedAt.Equal(returnedAt) {
		t.Errorf("returned_at: got %v, want %v", got.ReturnedAt, returnedAt)
	}
}

// Failure path: FailLogTail records the agent's error code + message.
// Content stays nil; status is "error".
func TestRegistryLogTailFailurePath(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-logtail-02", "33333333-3333-3333-3333-bbbbbbbbbbbb")

	corrID := "corr-logtail-fail"
	if err := srv.Registry.CreateLogTailRequest(ctx, registry.LogTailRequest{
		CorrelationID:  corrID,
		DeviceID:       deviceID,
		LogName:        "nonexistent",
		LinesRequested: 100,
		RequestedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateLogTailRequest: %v", err)
	}

	failedAt := time.Now().UTC()
	if err := srv.Registry.FailLogTail(ctx, registry.LogTailFailure{
		CorrelationID: corrID,
		ErrorCode:     "log_tail.unknown_log",
		ErrorMessage:  "log_name 'nonexistent' not in agent allow-list",
		ReturnedAt:    failedAt,
	}); err != nil {
		t.Fatalf("FailLogTail: %v", err)
	}

	got, err := srv.Registry.GetLogTail(ctx, corrID)
	if err != nil {
		t.Fatalf("GetLogTail after fail: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("status: got %q, want error", got.Status)
	}
	if got.Content != nil {
		t.Errorf("content: got %v, want nil on failure", got.Content)
	}
	if got.ErrorCode == nil || *got.ErrorCode != "log_tail.unknown_log" {
		t.Errorf("error_code: got %v", got.ErrorCode)
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != "log_name 'nonexistent' not in agent allow-list" {
		t.Errorf("error_message: got %v", got.ErrorMessage)
	}
}

// GetLogTail with an unknown correlation_id returns ErrLogTailNotFound
// so the API can return 404 cleanly (instead of 500 or empty body).
func TestRegistryGetLogTailNotFound(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	_, err := srv.Registry.GetLogTail(ctx, "corr-does-not-exist")
	if !errors.Is(err, registry.ErrLogTailNotFound) {
		t.Errorf("expected ErrLogTailNotFound, got %v", err)
	}
}

// Sweeper deletes rows whose requested_at is older than the threshold;
// fresh rows are preserved. Mirrors the device_services sweeper pattern
// (phase-2-followups issue 01).
func TestRegistryDeleteStaleLogTails(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-logtail-03", "33333333-3333-3333-3333-cccccccccccc")

	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)

	if err := srv.Registry.CreateLogTailRequest(ctx, registry.LogTailRequest{
		CorrelationID: "old", DeviceID: deviceID, LogName: "agent", LinesRequested: 100, RequestedAt: old,
	}); err != nil {
		t.Fatalf("create old: %v", err)
	}
	if err := srv.Registry.CreateLogTailRequest(ctx, registry.LogTailRequest{
		CorrelationID: "recent", DeviceID: deviceID, LogName: "agent", LinesRequested: 100, RequestedAt: recent,
	}); err != nil {
		t.Fatalf("create recent: %v", err)
	}

	threshold := time.Now().UTC().Add(-24 * time.Hour)
	n, err := srv.Registry.DeleteStaleLogTails(ctx, threshold)
	if err != nil {
		t.Fatalf("DeleteStaleLogTails: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted: got %d, want 1", n)
	}

	if _, err := srv.Registry.GetLogTail(ctx, "recent"); err != nil {
		t.Errorf("recent row should still exist: %v", err)
	}
	if _, err := srv.Registry.GetLogTail(ctx, "old"); !errors.Is(err, registry.ErrLogTailNotFound) {
		t.Errorf("old row should be gone: got %v", err)
	}
}
