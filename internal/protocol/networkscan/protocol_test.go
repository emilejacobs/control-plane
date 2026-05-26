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

// The Response on-wire shape: a list of candidate hosts. Each host has
// ip / mac / vendor (string, possibly empty) and a sorted list of
// open camera-relevant ports. JSON keys are snake_case to match the
// rest of the cmd protocol.
func TestResponseSerialization(t *testing.T) {
	resp := networkscan.Response{
		Hosts: []networkscan.Host{
			{
				IP:        "192.168.1.10",
				MAC:       "aa:bb:cc:dd:ee:ff",
				Vendor:    "Hikvision",
				OpenPorts: []int{80, 554},
			},
		},
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"hosts":[{"ip":"192.168.1.10","mac":"aa:bb:cc:dd:ee:ff","vendor":"Hikvision","open_ports":[80,554]}]}`
	if string(out) != want {
		t.Errorf("Marshal:\n got  %s\n want %s", out, want)
	}
	// Empty list serialises as an empty array, not null — dashboard
	// distinguishes "scan returned nothing" from a fetch failure.
	empty, _ := json.Marshal(networkscan.Response{Hosts: []networkscan.Host{}})
	if string(empty) != `{"hosts":[]}` {
		t.Errorf("empty hosts: got %s, want {\"hosts\":[]}", empty)
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
