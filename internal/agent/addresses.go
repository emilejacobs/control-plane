package agent

import (
	"net"
	"strings"
)

// InterfaceAddrs is the test seam shape — interface name plus the
// raw net.Addr list iface.Addrs() returns in production. The
// enumerator pattern mirrors edgeui.InterfaceEnumerator
// (DetectListenAddrs); we don't import that one to avoid an
// internal/agent ↔ internal/edgeui dependency cycle, and the raw
// net.Addr shape keeps the two helpers below close to the existing
// subnetCandidate in network_scanner.go.
type InterfaceAddrs struct {
	Name  string
	Addrs []net.Addr
}

// InterfaceAddrEnumerator is the seam the primary-address helpers
// probe against. Production wires SystemInterfaceAddrs; tests pass
// an in-memory fake.
type InterfaceAddrEnumerator interface {
	Interfaces() ([]InterfaceAddrs, error)
}

// SystemInterfaceAddrs wraps net.Interfaces() + iface.Addrs() into
// the seam shape above.
type SystemInterfaceAddrs struct{}

func (SystemInterfaceAddrs) Interfaces() ([]InterfaceAddrs, error) {
	raw, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]InterfaceAddrs, 0, len(raw))
	for _, ri := range raw {
		addrs, err := ri.Addrs()
		if err != nil {
			// Skip an interface whose addrs are unreadable — others
			// may still resolve cleanly.
			continue
		}
		out = append(out, InterfaceAddrs{Name: ri.Name, Addrs: addrs})
	}
	return out, nil
}

// cgnatNet is Tailscale's 100.64.0.0/10 CGNAT allocation. utun*
// (macOS) and tailscale0 (Linux) carry an IPv4 in this range when
// Tailscale is up. Defined as *net.IPNet here (rather than netip.Prefix
// like edgeui's listener.go) so it shares the *net.IPNet plumbing with
// subnetCandidate.
var cgnatNet = mustParseCIDR("100.64.0.0/10")

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// PrimaryRFC1918Addr returns the agent's primary RFC1918 IPv4
// address (the one telemetry publishes as devices.lan_ip), or ""
// when none can be detected. The heuristic walks every interface's
// IPv4 addresses and returns the first that:
//   - is private per isPrivateV4 (shared with subnetCandidate), AND
//   - is NOT in Tailscale's 100.64.0.0/10 CGNAT range.
//
// Loopback (127.0.0.0/8), link-local (169.254.0.0/16), and IPv6
// addresses are skipped because isPrivateV4 only accepts the three
// RFC1918 blocks.
func PrimaryRFC1918Addr(enum InterfaceAddrEnumerator) string {
	if enum == nil {
		enum = SystemInterfaceAddrs{}
	}
	ifaces, err := enum.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		for _, a := range iface.Addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			var a4 [4]byte
			copy(a4[:], ip4)
			if !isPrivateV4(a4) {
				continue
			}
			if cgnatNet.Contains(ip4) {
				continue
			}
			return ip4.String()
		}
	}
	return ""
}

// PrimaryTailscaleAddr returns the agent's tailnet IPv4 address
// (the one telemetry publishes as devices.tailscale_ip), or ""
// when Tailscale is not up or no CGNAT-shaped address is bound.
//
// Heuristic mirrors edgeui.DetectListenAddrs: only utun* (macOS)
// or tailscale0 (Linux) interfaces, and the address must be in
// 100.64.0.0/10. Other VPNs that use utun (Cisco AnyConnect,
// WireGuard with explicit naming, etc.) land outside CGNAT and are
// skipped.
func PrimaryTailscaleAddr(enum InterfaceAddrEnumerator) string {
	if enum == nil {
		enum = SystemInterfaceAddrs{}
	}
	ifaces, err := enum.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if !looksLikeTailnetInterface(iface.Name) {
			continue
		}
		for _, a := range iface.Addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			if cgnatNet.Contains(ip4) {
				return ip4.String()
			}
		}
	}
	return ""
}

// looksLikeTailnetInterface mirrors edgeui.looksLikeTailnetIface
// without importing it (cycle avoidance).
func looksLikeTailnetInterface(name string) bool {
	if name == "tailscale0" {
		return true
	}
	if strings.HasPrefix(name, "utun") {
		return true
	}
	return false
}
