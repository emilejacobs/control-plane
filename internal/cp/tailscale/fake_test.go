package tailscale_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/tailscale"
)

// The Fake satisfies Minter, records requests, and returns its canned key.
func TestFakeMinter(t *testing.T) {
	var m tailscale.Minter = tailscale.NewFake()
	key, err := m.MintAuthKey(context.Background(), tailscale.MintOptions{Tags: []string{"tag:edge-device"}})
	if err != nil {
		t.Fatalf("MintAuthKey: %v", err)
	}
	if key.Key != "tskey-auth-fake" {
		t.Errorf("canned key: got %q", key.Key)
	}
	f := m.(*tailscale.Fake)
	if len(f.Minted) != 1 || len(f.Minted[0].Tags) != 1 {
		t.Errorf("mint not recorded: %+v", f.Minted)
	}
}
