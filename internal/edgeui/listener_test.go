package edgeui

import (
	"net/netip"
	"testing"
)

// fakeIface is the test-only InterfaceEnumerator implementation. It
// emits the given list verbatim and lets tests assert on what
// DetectListenAddrs picks up.
type fakeIface struct {
	name  string
	addrs []netip.Prefix
}

type fakeEnum struct {
	ifaces []fakeIface
}

func (f *fakeEnum) Interfaces() ([]InterfaceInfo, error) {
	out := make([]InterfaceInfo, 0, len(f.ifaces))
	for _, i := range f.ifaces {
		out = append(out, InterfaceInfo{Name: i.name, Prefixes: i.addrs})
	}
	return out, nil
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return p
}

func TestDetectListenAddrs_AlwaysIncludesLoopback(t *testing.T) {
	enum := &fakeEnum{}
	addrs, err := DetectListenAddrs(enum)
	if err != nil {
		t.Fatalf("DetectListenAddrs: %v", err)
	}
	loop := netip.MustParseAddr("127.0.0.1")
	found := false
	for _, a := range addrs {
		if a == loop {
			found = true
		}
	}
	if !found {
		t.Fatalf("loopback 127.0.0.1 not in %v", addrs)
	}
}

func TestDetectListenAddrs_UtunWithCGNAT_Picked(t *testing.T) {
	// macOS shape: utun3 with a 100.64.0.0/10 (CGNAT) address — that's
	// Tailscale's interface allocation. Should be picked up.
	enum := &fakeEnum{
		ifaces: []fakeIface{
			{name: "lo0", addrs: []netip.Prefix{mustPrefix(t, "127.0.0.1/8")}},
			{name: "utun3", addrs: []netip.Prefix{mustPrefix(t, "100.95.1.42/32")}},
			{name: "en0", addrs: []netip.Prefix{mustPrefix(t, "192.168.1.50/24")}},
		},
	}
	addrs, err := DetectListenAddrs(enum)
	if err != nil {
		t.Fatalf("DetectListenAddrs: %v", err)
	}
	tailnet := netip.MustParseAddr("100.95.1.42")
	en0 := netip.MustParseAddr("192.168.1.50")
	hasTailnet, hasEn0 := false, false
	for _, a := range addrs {
		if a == tailnet {
			hasTailnet = true
		}
		if a == en0 {
			hasEn0 = true
		}
	}
	if !hasTailnet {
		t.Errorf("tailnet 100.95.1.42 not in %v", addrs)
	}
	if hasEn0 {
		t.Errorf("non-tailnet en0 192.168.1.50 must not be in %v (tailnet perimeter)", addrs)
	}
}

func TestDetectListenAddrs_LinuxTailscale0_Picked(t *testing.T) {
	// Linux shape: tailscale0 with a 100.64.0.0/10 address.
	enum := &fakeEnum{
		ifaces: []fakeIface{
			{name: "tailscale0", addrs: []netip.Prefix{mustPrefix(t, "100.64.5.7/32")}},
		},
	}
	addrs, err := DetectListenAddrs(enum)
	if err != nil {
		t.Fatalf("DetectListenAddrs: %v", err)
	}
	tailnet := netip.MustParseAddr("100.64.5.7")
	found := false
	for _, a := range addrs {
		if a == tailnet {
			found = true
		}
	}
	if !found {
		t.Fatalf("tailnet 100.64.5.7 not in %v", addrs)
	}
}

func TestDetectListenAddrs_NoTailnetInterface_LoopbackOnly(t *testing.T) {
	// Fail-open per ADR-032: never refuse to start, loopback-only is
	// acceptable when Tailscale isn't up yet.
	enum := &fakeEnum{
		ifaces: []fakeIface{
			{name: "en0", addrs: []netip.Prefix{mustPrefix(t, "192.168.1.50/24")}},
		},
	}
	addrs, err := DetectListenAddrs(enum)
	if err != nil {
		t.Fatalf("DetectListenAddrs: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected loopback-only, got %v", addrs)
	}
	if addrs[0] != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("expected 127.0.0.1, got %v", addrs[0])
	}
}

func TestDetectListenAddrs_UtunNonCGNAT_Skipped(t *testing.T) {
	// A utun without a 100.64.0.0/10 address is some other VPN — skip.
	enum := &fakeEnum{
		ifaces: []fakeIface{
			{name: "utun0", addrs: []netip.Prefix{mustPrefix(t, "10.99.1.1/32")}},
		},
	}
	addrs, err := DetectListenAddrs(enum)
	if err != nil {
		t.Fatalf("DetectListenAddrs: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected loopback-only when utun has no CGNAT address, got %v", addrs)
	}
}
