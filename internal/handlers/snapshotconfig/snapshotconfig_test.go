package snapshotconfig_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/handlers/snapshotconfig"
)

type fakeStore struct {
	got string
	err error
}

func (f *fakeStore) SetCadence(c string) error {
	f.got = c
	return f.err
}

func TestHandlePersistsCadence(t *testing.T) {
	store := &fakeStore{}
	h := snapshotconfig.New(store)
	out, err := h.Handle(context.Background(), json.RawMessage(`{"cadence":"daily"}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if store.got != "daily" {
		t.Errorf("persisted cadence = %q, want daily", store.got)
	}
	if out == nil {
		t.Error("expected an ACK result")
	}
}

func TestHandleRejectsBadCadence(t *testing.T) {
	h := snapshotconfig.New(&fakeStore{})
	_, err := h.Handle(context.Background(), json.RawMessage(`{"cadence":"hourly"}`))
	assertCoded(t, err, "snapshot_config.bad_payload")
}

func TestHandleSurfacesPersistFailure(t *testing.T) {
	h := snapshotconfig.New(&fakeStore{err: errors.New("disk full")})
	_, err := h.Handle(context.Background(), json.RawMessage(`{"cadence":"weekly"}`))
	assertCoded(t, err, "snapshot_config.persist_failed")
}

func assertCoded(t *testing.T, err error, want string) {
	t.Helper()
	var ce *envelope.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a CodedError", err)
	}
	if ce.Code != want {
		t.Errorf("code = %q, want %q", ce.Code, want)
	}
}
