package agent

import (
	"net"
	"testing"
)

// fakeAddr is the test-only InterfaceAddrEnumerator implementation.
// It builds *net.IPNet values from CIDR strings the cases provide
// (matching the shape iface.Addrs() returns in production).
type fakeIfaceAddrs struct {
	name  string
	addrs []string // CIDR-style strings (192.168.1.50/24, 100.64.5.7/32, …)
}

type fakeAddrEnum struct {
	ifaces []fakeIfaceAddrs
}

func (f *fakeAddrEnum) Interfaces() ([]InterfaceAddrs, error) {
	out := make([]InterfaceAddrs, 0, len(f.ifaces))
	for _, i := range f.ifaces {
		info := InterfaceAddrs{Name: i.name}
		for _, s := range i.addrs {
			ip, ipnet, err := net.ParseCIDR(s)
			if err != nil {
				continue
			}
			info.Addrs = append(info.Addrs, &net.IPNet{IP: ip, Mask: ipnet.Mask})
		}
		out = append(out, info)
	}
	return out, nil
}

// Bench-Mac shape (2026-05-26): a loopback, the Tailscale utun*
// CGNAT address, and en0 with a stock RFC1918. The two helpers must
// pick exactly the right address from this mix.
func TestPrimaryRFC1918Addr_BenchMacShape(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "lo0", addrs: []string{"127.0.0.1/8"}},
		{name: "utun3", addrs: []string{"100.122.190.107/32"}},
		{name: "en0", addrs: []string{"192.168.54.215/24"}},
	}}
	got := PrimaryRFC1918Addr(enum)
	if got != "192.168.54.215" {
		t.Errorf("PrimaryRFC1918Addr: got %q want 192.168.54.215", got)
	}
}

func TestPrimaryTailscaleAddr_BenchMacShape(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "lo0", addrs: []string{"127.0.0.1/8"}},
		{name: "utun3", addrs: []string{"100.122.190.107/32"}},
		{name: "en0", addrs: []string{"192.168.54.215/24"}},
	}}
	got := PrimaryTailscaleAddr(enum)
	if got != "100.122.190.107" {
		t.Errorf("PrimaryTailscaleAddr: got %q want 100.122.190.107", got)
	}
}

// Loopback-only host (e.g. pre-network early-boot, isolated dev
// loop): both helpers must return "" rather than picking a
// loopback or 169.254 address.
func TestPrimaryAddrs_LoopbackOnly(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "lo0", addrs: []string{"127.0.0.1/8"}},
	}}
	if got := PrimaryRFC1918Addr(enum); got != "" {
		t.Errorf("PrimaryRFC1918Addr (loopback-only): got %q want \"\"", got)
	}
	if got := PrimaryTailscaleAddr(enum); got != "" {
		t.Errorf("PrimaryTailscaleAddr (loopback-only): got %q want \"\"", got)
	}
}

// Tailscale CGNAT must NOT count as an RFC1918 LAN address — the
// 100.64.0.0/10 range is reserved CGNAT, not RFC1918. A device
// whose only non-loopback IPv4 is its tailnet address should
// surface tailscale_ip but leave lan_ip empty.
func TestPrimaryRFC1918Addr_SkipsCGNAT(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "utun3", addrs: []string{"100.122.190.107/32"}},
	}}
	if got := PrimaryRFC1918Addr(enum); got != "" {
		t.Errorf("PrimaryRFC1918Addr (CGNAT-only): got %q want \"\"", got)
	}
}

// A utun with a non-CGNAT address is some other VPN — must not be
// reported as the Tailscale address.
func TestPrimaryTailscaleAddr_SkipsNonCGNATUtun(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "utun0", addrs: []string{"10.99.1.1/32"}},
	}}
	if got := PrimaryTailscaleAddr(enum); got != "" {
		t.Errorf("PrimaryTailscaleAddr (non-CGNAT utun): got %q want \"\"", got)
	}
}

// Linux shape: tailscale0 is the canonical Tailscale interface
// name on Linux; the resolver must recognise it.
func TestPrimaryTailscaleAddr_LinuxTailscale0(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "tailscale0", addrs: []string{"100.64.5.7/32"}},
	}}
	got := PrimaryTailscaleAddr(enum)
	if got != "100.64.5.7" {
		t.Errorf("PrimaryTailscaleAddr (linux): got %q want 100.64.5.7", got)
	}
}
