package ingest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
)

type fakeDeviceServicesSweeperWriter struct {
	mu        sync.Mutex
	calls     []time.Time
	returnN   int
	returnErr error
}

func (w *fakeDeviceServicesSweeperWriter) DeleteStaleDeviceServices(_ context.Context, olderThan time.Time) (int, error) {
	w.mu.Lock()
	w.calls = append(w.calls, olderThan)
	w.mu.Unlock()
	return w.returnN, w.returnErr
}

// Sweeper passes cutoff = now - threshold to the writer.
func TestDeviceServicesSweeperComputesCutoff(t *testing.T) {
	now := time.Date(2026, 5, 24, 22, 0, 0, 0, time.UTC)
	w := &fakeDeviceServicesSweeperWriter{returnN: 3}
	s := ingest.NewDeviceServicesSweeper(w, ingest.DeviceServicesSweeperConfig{
		Interval:  10 * time.Millisecond,
		Threshold: 15 * time.Minute,
		Now:       func() time.Time { return now },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(25 * time.Millisecond)
	cancel()
	<-done

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) == 0 {
		t.Fatal("expected ≥1 DeleteStaleDeviceServices call")
	}
	want := now.Add(-15 * time.Minute)
	if !w.calls[0].Equal(want) {
		t.Errorf("cutoff: got %v, want %v", w.calls[0], want)
	}
}

// Writer error doesn't kill the sweeper — it logs and keeps ticking.
func TestDeviceServicesSweeperContinuesOnWriterError(t *testing.T) {
	w := &fakeDeviceServicesSweeperWriter{returnErr: errors.New("transient db error")}
	s := ingest.NewDeviceServicesSweeper(w, ingest.DeviceServicesSweeperConfig{
		Interval:  5 * time.Millisecond,
		Threshold: 15 * time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) < 2 {
		t.Errorf("expected ≥2 calls despite errors; got %d", len(w.calls))
	}
}
