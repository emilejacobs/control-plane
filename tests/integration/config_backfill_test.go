package integration_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/authn"
	"github.com/emilejacobs/control-plane/internal/cp/api"
	configbackfillproto "github.com/emilejacobs/control-plane/internal/protocol/configbackfill"
)

// TestConfigBackfillPublishes covers the HTTP backfill path: a staff POST
// /devices/{id}/config-backfill publishes a config.backfill cmd carrying the
// standard snapshot_state_path. A replay with the same Idempotency-Key does
// not re-publish (ADR-012).
func TestConfigBackfillPublishes(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pub := newCommissionPublisher()
	srv := buildTestServerWith(t, ctx, startPostgres(t, ctx, nil), authn.Config{}, func(d *api.Deps) {
		d.CmdPublisher = pub
	})

	deviceID := enrollForTest(t, srv, "mac-mini-bf-01", "c14a6bc9-d702-587a-95f8-522cb618f1cc")
	token := mintAccessToken(t, ctx, srv)

	post := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/devices/"+deviceID+"/config-backfill", bytes.NewReader([]byte("")))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "backfill-"+deviceID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}

	if code := post(); code != http.StatusAccepted {
		t.Fatalf("config-backfill status: got %d want 202", code)
	}

	cmd, ok := pub.byType["config.backfill"]
	if !ok {
		t.Fatal("config.backfill not published")
	}
	args, err := configbackfillproto.ParseArgs(cmd.Args)
	if err != nil {
		t.Fatalf("config.backfill args invalid: %v", err)
	}
	if args.SnapshotStatePath != "/var/uknomi/snapshot-state.json" {
		t.Errorf("snapshot_state_path: got %q", args.SnapshotStatePath)
	}

	// Idempotent replay: same key → no second publish.
	if code := post(); code != http.StatusAccepted {
		t.Fatalf("replay status: got %d want 202", code)
	}
	if pub.typeCount["config.backfill"] != 1 {
		t.Errorf("config.backfill published %d times, want 1 (idempotent)", pub.typeCount["config.backfill"])
	}
}
