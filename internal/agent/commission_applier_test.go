package agent

import (
	"context"
	"errors"
	"testing"
)

func TestCommissionApplierJoinTailnet(t *testing.T) {
	var got []string
	a := &commissionApplier{tsUp: func(_ context.Context, args ...string) error {
		got = args
		return nil
	}}
	if err := a.JoinTailnet(context.Background(), "tskey-z"); err != nil {
		t.Fatalf("JoinTailnet: %v", err)
	}
	if len(got) != 2 || got[0] != "up" || got[1] != "--authkey=tskey-z" {
		t.Errorf("tailscale args: got %v want [up --authkey=tskey-z]", got)
	}
}

func TestCommissionApplierJoinTailnetError(t *testing.T) {
	a := &commissionApplier{tsUp: func(context.Context, ...string) error { return errors.New("boom") }}
	if err := a.JoinTailnet(context.Background(), "k"); err == nil {
		t.Fatal("expected error when tailscale up fails")
	}
}

func TestCommissionApplierStartALPRUnavailable(t *testing.T) {
	a := &commissionApplier{} // no alprStart wired
	if err := a.StartALPR(context.Background(), "l", "t"); err == nil {
		t.Fatal("expected error when ALPR is unavailable")
	}
}

func TestCommissionApplierStartALPRDelegates(t *testing.T) {
	var gotLic, gotTok string
	a := &commissionApplier{alprStart: func(_ context.Context, license, token string) error {
		gotLic, gotTok = license, token
		return nil
	}}
	if err := a.StartALPR(context.Background(), "LIC", "TOK"); err != nil {
		t.Fatalf("StartALPR: %v", err)
	}
	if gotLic != "LIC" || gotTok != "TOK" {
		t.Errorf("delegated args: got %q/%q", gotLic, gotTok)
	}
}
