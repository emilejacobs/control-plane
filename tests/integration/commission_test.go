package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/emilejacobs/control-plane/internal/cp/api"
	"github.com/emilejacobs/control-plane/internal/cp/commission"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/tailscale"
	"github.com/emilejacobs/control-plane/internal/cp/taxonomy"
	"github.com/emilejacobs/control-plane/internal/envelope"
	commissionproto "github.com/emilejacobs/control-plane/internal/protocol/commission"
	"github.com/emilejacobs/control-plane/internal/cp/authn"
)

// commissionPublisher records every command published to a device topic so the
// test can assert the Commission fan-out.
type commissionPublisher struct {
	mu        sync.Mutex
	byType    map[string]envelope.Command
	typeCount map[string]int
}

func newCommissionPublisher() *commissionPublisher {
	return &commissionPublisher{byType: map[string]envelope.Command{}, typeCount: map[string]int{}}
}

func (p *commissionPublisher) Publish(_ context.Context, _ string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var cmd envelope.Command
	_ = json.Unmarshal(payload, &cmd)
	p.byType[cmd.Type] = cmd
	p.typeCount[cmd.Type]++
	return nil
}

// TestCommissionFanOut covers the HTTP Commission path end-to-end: a staff POST
// on an assigned ALPR device mints a key and publishes cameras.update + the
// commission cmd (with the minted key + ALPR license/token). A replay with the
// same Idempotency-Key does not re-publish (ADR-012).
func TestCommissionFanOut(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	pub := newCommissionPublisher()
	minter := tailscale.NewFake()
	minter.KeyToReturn = tailscale.Key{Key: "tskey-auth-minted"}

	srv := buildTestServerWith(t, ctx, startPostgres(t, ctx, nil), authn.Config{}, func(d *api.Deps) {
		d.CmdPublisher = pub
		d.Commissioner = commission.New(d.Registry, minter, pub,
			commission.Config{Tailnet: "uknomi.org", TailscaleTags: []string{"tag:edge-device"}, TailscaleExpirySeconds: 3600},
			func() string { return "cmd-id" })
	})

	// Seed a site, enroll a device, assign it, give it an ALPR license + token.
	store := taxonomy.NewStore(srv.Pool)
	synced := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	cid, _ := store.UpsertClient(ctx, taxonomy.ClientRow{ExternalID: "1", Name: "Client #1", SyncedAt: synced})
	siteID, _ := store.UpsertSite(ctx, taxonomy.SiteRow{
		ExternalID: "1", Name: "Site #1", ClientID: cid, BrandName: "BK", BrandExternalID: "1", Active: true, SyncedAt: synced,
	})
	deviceID := enrollForTest(t, srv, "07-eegees-mesa-macmini", "b04a6bc9-d702-587a-95f8-522cb618f1aa")
	sctx := staffCtx(ctx)
	if err := srv.Registry.SetDeployment(sctx, deviceID, &siteID, nil); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := srv.Registry.SetALPRLicense(sctx, deviceID, "ALPR-LIC"); err != nil {
		t.Fatalf("set license: %v", err)
	}
	if err := srv.Registry.SetCPSetting(sctx, registry.SettingPlateRecognizerToken, "PR-TOKEN"); err != nil {
		t.Fatalf("set token: %v", err)
	}

	token := mintAccessToken(t, ctx, srv)
	post := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/devices/"+deviceID+"/commission", bytes.NewReader([]byte("")))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "commission-"+deviceID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}

	if code := post(); code != http.StatusAccepted {
		t.Fatalf("commission status: got %d want 202", code)
	}

	// Fan-out: cameras.update + commission, both published once.
	commCmd, ok := pub.byType["commission"]
	if !ok {
		t.Fatal("commission cmd not published")
	}
	if _, ok := pub.byType["cameras.update"]; !ok {
		t.Error("cameras.update not published")
	}
	args, err := commissionproto.ParseArgs(commCmd.Args)
	if err != nil {
		t.Fatalf("commission args invalid: %v", err)
	}
	if args.TailscaleAuthKey != "tskey-auth-minted" {
		t.Errorf("auth key: got %q", args.TailscaleAuthKey)
	}
	if args.ALPR == nil || args.ALPR.License != "ALPR-LIC" || args.ALPR.Token != "PR-TOKEN" {
		t.Errorf("ALPR: got %+v", args.ALPR)
	}

	// Idempotent replay: same key → no second publish.
	if code := post(); code != http.StatusAccepted {
		t.Fatalf("replay status: got %d want 202", code)
	}
	if pub.typeCount["commission"] != 1 {
		t.Errorf("commission published %d times across replay, want 1 (idempotent)", pub.typeCount["commission"])
	}
}
