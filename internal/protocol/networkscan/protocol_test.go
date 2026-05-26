package networkscan_test

import (
	"encoding/json"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
)

// ValidateCIDR enforces a strict v4 /24-ish shape: an IPv4 CIDR string
// the agent can hand to its scanner. Reject empty, malformed, IPv6 — the
// scanner is v4-only by design (store LANs in 2026 are still v4).
func TestValidateCIDR(t *testing.T) {
	cases := []struct {
		name     string
		cidr     string
		wantCode string // empty = success
	}{
		{"happy /24", "192.168.1.0/24", ""},
		{"happy /16", "10.0.0.0/16", ""},
		{"empty string", "", networkscan.CodeBadCIDR},
		{"missing slash", "192.168.1.0", networkscan.CodeBadCIDR},
		{"out-of-range octet", "300.0.0.0/24", networkscan.CodeBadCIDR},
		{"ipv6 rejected", "2001:db8::/32", networkscan.CodeBadCIDR},
		{"garbage", "not-a-cidr", networkscan.CodeBadCIDR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := networkscan.ValidateCIDR(tc.cidr)
			if tc.wantCode == "" {
				if err != nil {
					t.Errorf("got error %v, want success", err)
				}
				return
			}
			v, ok := networkscan.AsValidation(err)
			if !ok {
				t.Fatalf("got error %v (type %T), want *ValidationError", err, err)
			}
			if v.Code != tc.wantCode {
				t.Errorf("code: got %q, want %q", v.Code, tc.wantCode)
			}
		})
	}
}

// ParseRequest accepts an empty payload (auto-detect mode) and a payload
// carrying a single optional `cidr` override. Unknown top-level fields
// are rejected with CodeUnknownField per ADR-028's protective stance.
func TestParseRequest(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantCode string // empty = success
		wantCIDR string
	}{
		{"empty object means auto-detect", `{}`, "", ""},
		{"empty bytes also auto-detect", ``, "", ""},
		{"happy override", `{"cidr":"192.168.1.0/24"}`, "", "192.168.1.0/24"},
		{"bad cidr rejected", `{"cidr":"nope"}`, networkscan.CodeBadCIDR, ""},
		{"unknown field rejected", `{"cidr":"10.0.0.0/24","subnet":"x"}`, networkscan.CodeUnknownField, ""},
		{"malformed json rejected", `{`, networkscan.CodeUnknownField, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := networkscan.ParseRequest(json.RawMessage(tc.raw))
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("got error %v, want success", err)
				}
				if req.CIDR != tc.wantCIDR {
					t.Errorf("CIDR: got %q, want %q", req.CIDR, tc.wantCIDR)
				}
				return
			}
			v, ok := networkscan.AsValidation(err)
			if !ok {
				t.Fatalf("got %v (type %T), want *ValidationError", err, err)
			}
			if v.Code != tc.wantCode {
				t.Errorf("code: got %q, want %q", v.Code, tc.wantCode)
			}
		})
	}
}
