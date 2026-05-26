package agent

import (
	"context"
	"errors"
	"net"
	"sort"
	"testing"
)

// Exercise the host-discovery parser. The fixture mirrors the shape of
// `nmap -sn -PR --open` on a small LAN: one host with a MAC, one
// without (rare; usually the agent's own host).
func TestParseNmapOutput(t *testing.T) {
	fixture := []byte(`
Starting Nmap 7.94 ( https://nmap.org ) at 2026-05-26 12:00 PDT
Nmap scan report for 192.168.1.1
Host is up (0.0010s latency).
MAC Address: 00:11:22:33:44:55 (Some Router)
Nmap scan report for cam-1.local (192.168.1.42)
Host is up (0.0020s latency).
MAC Address: 44:19:B6:AA:BB:CC (Hikvision Digital Technology)
Nmap scan report for 192.168.1.99
Host is up (0.0030s latency).
Nmap done: 256 IP addresses (3 hosts up) scanned in 1.20 seconds
`)
	got := parseNmapOutput(fixture)
	if len(got) != 3 {
		t.Fatalf("hosts: got %d, want 3", len(got))
	}
	wantIPs := []string{"192.168.1.1", "192.168.1.42", "192.168.1.99"}
	for i, h := range got {
		if h.IP != wantIPs[i] {
			t.Errorf("host[%d].IP: got %q, want %q", i, h.IP, wantIPs[i])
		}
	}
	if got[1].MAC != "44:19:B6:AA:BB:CC" {
		t.Errorf("host[1].MAC: got %q", got[1].MAC)
	}
	if got[2].MAC != "" {
		t.Errorf("host[2].MAC: got %q, want empty", got[2].MAC)
	}
}

// Parse greppable port output: each line is one host, ports are
// extracted and de-duplicated.
func TestParseNmapGreppablePorts(t *testing.T) {
	fixture := []byte(`# Nmap 7.94 scan initiated
Host: 192.168.1.42 ()	Ports: 80/open/tcp//http///, 554/open/tcp//rtsp///	Ignored State: closed
Host: 192.168.1.43 ()	Ports: 443/open/tcp//https///	Ignored State: closed
# Nmap done
`)
	got := parseNmapGreppablePorts(fixture)
	if len(got) != 2 {
		t.Fatalf("hosts: got %d, want 2", len(got))
	}
	if !equalIntSet(got["192.168.1.42"], []int{80, 554}) {
		t.Errorf("ports for .42: got %v, want [80 554]", got["192.168.1.42"])
	}
	if !equalIntSet(got["192.168.1.43"], []int{443}) {
		t.Errorf("ports for .43: got %v, want [443]", got["192.168.1.43"])
	}
}

// nmapScanner shells out via runFunc — injecting a fake exercises the
// happy path without touching the real subprocess.
func TestNmapScannerScanInjectedFake(t *testing.T) {
	calls := 0
	sc := &nmapScanner{
		timeout: scanTimeoutDefault,
		runFunc: func(_ context.Context, args ...string) ([]byte, error) {
			calls++
			// First call: host discovery. Second call: port scan.
			if calls == 1 {
				return []byte(`
Nmap scan report for 192.168.1.42
MAC Address: 44:19:B6:AA:BB:CC (Hikvision)
Nmap done: 1 host up
`), nil
			}
			return []byte(`Host: 192.168.1.42 ()	Ports: 554/open/tcp//rtsp///, 80/open/tcp//http///
`), nil
		},
	}
	got, err := sc.Scan(context.Background(), "192.168.1.0/24")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if calls != 2 {
		t.Errorf("runFunc calls: got %d, want 2", calls)
	}
	if len(got) != 1 || got[0].IP != "192.168.1.42" || got[0].MAC != "44:19:B6:AA:BB:CC" {
		t.Errorf("host: %+v", got)
	}
	sort.Ints(got[0].OpenPorts)
	if !equalIntSet(got[0].OpenPorts, []int{80, 554}) {
		t.Errorf("ports: got %v, want [80 554]", got[0].OpenPorts)
	}
}

// runFunc failure on host discovery surfaces from Scan; no port scan
// attempted. The agent handler wraps this as CodeScanFailed.
func TestNmapScannerScanReturnsRunErr(t *testing.T) {
	sc := &nmapScanner{
		timeout: scanTimeoutDefault,
		runFunc: func(_ context.Context, _ ...string) ([]byte, error) {
			return nil, errors.New("exit status 2")
		},
	}
	_, err := sc.Scan(context.Background(), "10.0.0.0/24")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// Regression: macOS returns iface.Addrs() *net.IPNet whose .IP is a
// 16-byte IPv4-mapped slice. The previous heuristic used netip's
// AddrFromSlice + Is4, which treats 16-byte input as IPv6 and rejected
// every macOS interface — surfacing as "no suitable IPv4 interface"
// on devices with valid 192.168.x.x en0 (bench Mac 2026-05-26).
func TestSubnetCandidate(t *testing.T) {
	cases := []struct {
		name   string
		ip     net.IP
		want   string
		wantOK bool
	}{
		{"linux 4-byte private", net.IPv4(192, 168, 54, 215).To4(), "192.168.54.0/24", true},
		{"macOS 16-byte IPv4-mapped private", net.IPv4(192, 168, 54, 215), "192.168.54.0/24", true},
		{"10/8 private", net.IPv4(10, 1, 2, 3).To4(), "10.1.2.0/24", true},
		{"tailscale 100.x CGNAT", net.IPv4(100, 122, 190, 107).To4(), "", false},
		{"link-local 169.254", net.IPv4(169, 254, 1, 2).To4(), "", false},
		{"IPv6 native", net.ParseIP("fe80::1"), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ipnet := &net.IPNet{IP: c.ip, Mask: net.CIDRMask(24, 32)}
			got, ok := subnetCandidate(ipnet)
			if ok != c.wantOK {
				t.Errorf("ok: got %v, want %v", ok, c.wantOK)
			}
			if got != c.want {
				t.Errorf("subnet: got %q, want %q", got, c.want)
			}
		})
	}
}

// isPrivateV4 truth table — auto-detect logic depends on this.
func TestIsPrivateV4(t *testing.T) {
	cases := []struct {
		ip   [4]byte
		want bool
	}{
		{[4]byte{10, 0, 0, 1}, true},
		{[4]byte{172, 16, 0, 1}, true},
		{[4]byte{172, 31, 255, 254}, true},
		{[4]byte{172, 32, 0, 1}, false},
		{[4]byte{192, 168, 1, 1}, true},
		{[4]byte{8, 8, 8, 8}, false},
		{[4]byte{127, 0, 0, 1}, false},
	}
	for _, c := range cases {
		got := isPrivateV4(c.ip)
		if got != c.want {
			t.Errorf("isPrivateV4(%v): got %v, want %v", c.ip, got, c.want)
		}
	}
}

func equalIntSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Ints(a)
	sort.Ints(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
