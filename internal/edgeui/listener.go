package edgeui

import (
	"net"
	"net/netip"
	"strings"
)

// InterfaceInfo is the test seam shape — name + the netip.Prefix list
// from iface.Addrs(). The production enumerator (SystemInterfaces)
// wraps net.Interfaces() and converts addrs into this shape; tests
// build fakes against the same interface so DetectListenAddrs is
// exercised without touching the host's real interfaces.
type InterfaceInfo struct {
	Name     string
	Prefixes []netip.Prefix
}

// InterfaceEnumerator is the seam DetectListenAddrs probes against.
// Production wires SystemInterfaces; tests pass an in-memory fake.
type InterfaceEnumerator interface {
	Interfaces() ([]InterfaceInfo, error)
}

// cgnat is Tailscale's 100.64.0.0/10 allocation — both macOS utun*
// interfaces and Linux tailscale0 land an address in this range.
// Probing for it lets us pick the right interface without parsing
// `tailscale status` output (which would add another subprocess
// dependency we don't need).
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// DetectListenAddrs returns the IPv4 addresses the Edge UI binary
// binds. 127.0.0.1 is always present; the tailnet interface address
// is appended best-effort. Fail-open per ADR-032: a missing tailnet
// interface returns loopback-only without error.
//
// The Tailscale-interface heuristic looks for two shapes:
//   - macOS: any utun* interface with a 100.64.0.0/10 address.
//   - Linux: tailscale0 with a 100.64.0.0/10 address.
//
// Other VPNs that use utun (Cisco AnyConnect, WireGuard with explicit
// naming, etc.) are skipped — their addresses are outside CGNAT.
func DetectListenAddrs(enum InterfaceEnumerator) ([]netip.Addr, error) {
	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1")}

	ifaces, err := enum.Interfaces()
	if err != nil {
		// Fail-open: log-and-continue at the caller; this layer just
		// surfaces the error if the host's interface enumeration
		// itself failed (very rare).
		return addrs, err
	}
	for _, iface := range ifaces {
		if !looksLikeTailnetIface(iface.Name) {
			continue
		}
		for _, p := range iface.Prefixes {
			a := p.Addr()
			if !a.Is4() {
				continue
			}
			if cgnat.Contains(a) {
				addrs = append(addrs, a)
			}
		}
	}
	return addrs, nil
}

// looksLikeTailnetIface is the name-shape filter — Tailscale uses
// utun* on macOS and tailscale0 on Linux.
func looksLikeTailnetIface(name string) bool {
	if name == "tailscale0" {
		return true
	}
	if strings.HasPrefix(name, "utun") {
		return true
	}
	return false
}

// SystemInterfaces is the production enumerator: it wraps
// net.Interfaces() and converts iface.Addrs() into netip.Prefix.
type SystemInterfaces struct{}

// Interfaces implements InterfaceEnumerator. Mirrors the IPv4-mapped
// slice handling pattern from internal/agent/network_scanner.go's
// subnetCandidate — macOS returns IPv4 addresses as 16-byte
// IPv4-mapped slices, and netip.AddrFromSlice handles both shapes.
func (SystemInterfaces) Interfaces() ([]InterfaceInfo, error) {
	raw, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]InterfaceInfo, 0, len(raw))
	for _, ri := range raw {
		info := InterfaceInfo{Name: ri.Name}
		addrs, err := ri.Addrs()
		if err != nil {
			// Skip an interface whose addrs are unreadable — others
			// may still resolve cleanly.
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil {
				continue
			}
			addr, ok := netip.AddrFromSlice(ip4)
			if !ok {
				continue
			}
			ones, _ := ipn.Mask.Size()
			info.Prefixes = append(info.Prefixes, netip.PrefixFrom(addr, ones))
		}
		out = append(out, info)
	}
	return out, nil
}
