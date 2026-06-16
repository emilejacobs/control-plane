package commission_test

import (
	"encoding/json"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/commission"
)

func TestParseArgsTailscaleOnly(t *testing.T) {
	raw := json.RawMessage(`{"tailscale_auth_key":"tskey-auth-abc"}`)
	a, err := commission.ParseArgs(raw)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if a.TailscaleAuthKey != "tskey-auth-abc" {
		t.Errorf("auth key: got %q", a.TailscaleAuthKey)
	}
	if a.ALPR != nil {
		t.Errorf("ALPR should be nil when omitted: %+v", a.ALPR)
	}
}

func TestParseArgsWithALPR(t *testing.T) {
	raw := json.RawMessage(`{"tailscale_auth_key":"tskey-x","alpr":{"license":"LIC","token":"TOK"}}`)
	a, err := commission.ParseArgs(raw)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if a.ALPR == nil || a.ALPR.License != "LIC" || a.ALPR.Token != "TOK" {
		t.Errorf("ALPR: got %+v", a.ALPR)
	}
}

func TestParseArgsRejectsUnknownFields(t *testing.T) {
	raw := json.RawMessage(`{"tailscale_auth_key":"x","bogus":true}`)
	if _, err := commission.ParseArgs(raw); err == nil {
		t.Fatal("expected error on unknown field")
	}
}

func TestParseArgsRequiresAuthKey(t *testing.T) {
	raw := json.RawMessage(`{"alpr":{"license":"LIC","token":"TOK"}}`)
	if _, err := commission.ParseArgs(raw); err == nil {
		t.Fatal("expected error when tailscale_auth_key is empty")
	}
}

// An ALPR block missing license or token is invalid (avoids starting the
// container with a half-config).
func TestParseArgsRejectsIncompleteALPR(t *testing.T) {
	raw := json.RawMessage(`{"tailscale_auth_key":"x","alpr":{"license":"LIC"}}`)
	if _, err := commission.ParseArgs(raw); err == nil {
		t.Fatal("expected error when ALPR token is empty")
	}
}
