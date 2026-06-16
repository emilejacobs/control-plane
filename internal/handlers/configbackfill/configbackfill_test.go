package configbackfill_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	handler "github.com/emilejacobs/control-plane/internal/handlers/configbackfill"
	proto "github.com/emilejacobs/control-plane/internal/protocol/configbackfill"
)

type fakeApplier struct {
	got proto.Args
	err error
}

func (f *fakeApplier) Apply(_ context.Context, args proto.Args) error {
	f.got = args
	return f.err
}

func TestHandleApplies(t *testing.T) {
	fa := &fakeApplier{}
	res, err := handler.New(fa).Handle(context.Background(), json.RawMessage(`{"snapshot_state_path":"/var/uknomi/snapshot-state.json"}`))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if fa.got.SnapshotStatePath != "/var/uknomi/snapshot-state.json" {
		t.Errorf("applier got: %+v", fa.got)
	}
	if !res.(handler.Response).TakesEffectOnRestart {
		t.Error("expected TakesEffectOnRestart true")
	}
}

func TestHandleBadPayload(t *testing.T) {
	if _, err := handler.New(&fakeApplier{}).Handle(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected coded error on empty backfill")
	}
}

func TestHandleApplyFailure(t *testing.T) {
	fa := &fakeApplier{err: errors.New("disk full")}
	if _, err := handler.New(fa).Handle(context.Background(), json.RawMessage(`{"snapshot_state_path":"/x"}`)); err == nil {
		t.Fatal("expected error when Apply fails")
	}
}
