package commission_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/commission"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/tailscale"
	"github.com/emilejacobs/control-plane/internal/envelope"
	commissionproto "github.com/emilejacobs/control-plane/internal/protocol/commission"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

type fakeStore struct {
	dev      registry.Device
	devErr   error
	license  string
	token    string
	tokenSet bool
	cams     []cameras.Camera
}

func (f *fakeStore) GetByID(_ context.Context, _ string) (registry.Device, error) {
	return f.dev, f.devErr
}
func (f *fakeStore) GetALPRLicense(_ context.Context, _ string) (string, error) {
	return f.license, nil
}
func (f *fakeStore) GetCPSetting(_ context.Context, _ string) (string, bool, error) {
	return f.token, f.tokenSet, nil
}
func (f *fakeStore) ListCameras(_ context.Context, _ string) ([]cameras.Camera, error) {
	return f.cams, nil
}

type fakePublisher struct {
	published map[string]envelope.Command // type -> command
	topics    []string
}

func (p *fakePublisher) Publish(_ context.Context, topic string, payload []byte) error {
	if p.published == nil {
		p.published = map[string]envelope.Command{}
	}
	var cmd envelope.Command
	_ = json.Unmarshal(payload, &cmd)
	p.published[cmd.Type] = cmd
	p.topics = append(p.topics, topic)
	return nil
}

func site() *string { s := "11111111-1111-1111-1111-111111111111"; return &s }

func newService(store *fakeStore, minter tailscale.Minter, pub *fakePublisher) *commission.Service {
	n := 0
	return commission.New(store, minter, pub, commission.Config{
		Tailnet:                "uknomi.org",
		TailscaleTags:          []string{"tag:edge-device"},
		TailscaleExpirySeconds: 3600,
	}, func() string { n++; return "id-" + string(rune('0'+n)) })
}

// An assigned ALPR device: mint a key, push cameras.update + a commission cmd
// carrying the key + ALPR license/token.
func TestCommissionALPRDevice(t *testing.T) {
	store := &fakeStore{
		dev:      registry.Device{ID: "dev-1", SiteID: site()},
		license:  "ALPR-LIC",
		token:    "PR-TOKEN",
		tokenSet: true,
		cams:     []cameras.Camera{{CameraID: "cam1", RtspURL: "rtsp://a", IsLPR: true}},
	}
	minter := tailscale.NewFake()
	minter.KeyToReturn = tailscale.Key{Key: "tskey-auth-minted"}
	pub := &fakePublisher{}

	res, err := newService(store, minter, pub).Commission(context.Background(), "dev-1", "corr-1")
	if err != nil {
		t.Fatalf("Commission: %v", err)
	}
	if res.CorrelationID != "corr-1" {
		t.Errorf("correlation id: got %q", res.CorrelationID)
	}

	// Minted a per-device single-use tagged key.
	if len(minter.Minted) != 1 || len(minter.Minted[0].Tags) != 1 {
		t.Errorf("minter not called with tags: %+v", minter.Minted)
	}

	// Pushed cameras.update.
	camsCmd, ok := pub.published["cameras.update"]
	if !ok {
		t.Fatal("cameras.update not published")
	}
	var camReq cameras.UpdateAllRequest
	_ = json.Unmarshal(camsCmd.Args, &camReq)
	if len(camReq.Cameras) != 1 || camReq.Cameras[0].CameraID != "cam1" {
		t.Errorf("cameras.update args: %+v", camReq)
	}

	// Pushed the commission cmd with the key + ALPR.
	commCmd, ok := pub.published["commission"]
	if !ok {
		t.Fatal("commission cmd not published")
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
	// Both on the device cmd topic.
	for _, topic := range pub.topics {
		if !strings.Contains(topic, "devices/dev-1/cmd") {
			t.Errorf("unexpected topic: %s", topic)
		}
	}
}

// A non-ALPR device (no license): commission cmd carries the key, ALPR nil.
func TestCommissionNonALPRDevice(t *testing.T) {
	store := &fakeStore{dev: registry.Device{ID: "dev-2", SiteID: site()}}
	pub := &fakePublisher{}

	if _, err := newService(store, tailscale.NewFake(), pub).Commission(context.Background(), "dev-2", "c"); err != nil {
		t.Fatalf("Commission: %v", err)
	}
	args, _ := commissionproto.ParseArgs(pub.published["commission"].Args)
	if args.ALPR != nil {
		t.Errorf("ALPR should be nil for a device with no license: %+v", args.ALPR)
	}
}

// An unassigned device cannot be commissioned — no key minted, nothing published.
func TestCommissionUnassignedDevice(t *testing.T) {
	store := &fakeStore{dev: registry.Device{ID: "dev-3", SiteID: nil}}
	minter := tailscale.NewFake()
	pub := &fakePublisher{}

	_, err := newService(store, minter, pub).Commission(context.Background(), "dev-3", "c")
	if !errors.Is(err, commission.ErrNotAssigned) {
		t.Fatalf("got %v, want ErrNotAssigned", err)
	}
	if len(minter.Minted) != 0 || len(pub.topics) != 0 {
		t.Error("nothing should be minted or published for an unassigned device")
	}
}

// License set but no account PR token → refuse (would start the container with
// a half-config).
func TestCommissionLicenseWithoutToken(t *testing.T) {
	store := &fakeStore{dev: registry.Device{ID: "dev-4", SiteID: site()}, license: "LIC", tokenSet: false}
	pub := &fakePublisher{}
	_, err := newService(store, tailscale.NewFake(), pub).Commission(context.Background(), "dev-4", "c")
	if !errors.Is(err, commission.ErrNoPRToken) {
		t.Fatalf("got %v, want ErrNoPRToken", err)
	}
}

// A missing device propagates the registry's not-found error.
func TestCommissionDeviceNotFound(t *testing.T) {
	store := &fakeStore{devErr: registry.ErrDeviceNotFound}
	_, err := newService(store, tailscale.NewFake(), &fakePublisher{}).Commission(context.Background(), "x", "c")
	if !errors.Is(err, registry.ErrDeviceNotFound) {
		t.Fatalf("got %v, want ErrDeviceNotFound", err)
	}
}
