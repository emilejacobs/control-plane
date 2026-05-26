package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emilejacobs/control-plane/internal/handlers/networkscan"
)

// scanTimeoutDefault caps the scanner's wall-clock budget to fit the
// 30s acceptance ceiling in issue #3. The handler's ctx may shorten
// this further; nmap's "max-rate" args keep the subprocess from
// chewing the full timeout on a stalled probe.
const scanTimeoutDefault = 25 * time.Second

// nmapScanner is the production Scanner implementation: shells out to
// `nmap -sn -PR --open` on the supplied CIDR (or the auto-detected
// primary IPv4 subnet) and parses the greppable output. nmap is the
// install-module 07 dependency; the agent assumes it's on PATH.
//
// Two seams keep this testable in unit + integration tests:
//   - The runFunc field lets tests inject a fake subprocess runner;
//     production uses runNmap which actually shells out.
//   - The Scanner interface in the handler package means the agent
//     wires *any* implementation — see agent_test.go for the in-memory
//     fake that exercises the dispatcher integration.
type nmapScanner struct {
	timeout time.Duration
	runFunc func(ctx context.Context, args ...string) ([]byte, error)
}

func newNmapScanner() *nmapScanner {
	return &nmapScanner{timeout: scanTimeoutDefault, runFunc: runNmap}
}

func (s *nmapScanner) Scan(ctx context.Context, cidr string) ([]networkscan.RawHost, error) {
	target := cidr
	if target == "" {
		detected, err := detectPrimarySubnet()
		if err != nil {
			return nil, fmt.Errorf("auto-detect subnet: %w", err)
		}
		target = detected
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// -sn  ping-only, no port scan (fast host discovery)
	// -PR  ARP ping (LAN; nmap upgrades to this automatically on
	//      local subnets but explicit is safer)
	// --open  only show responding hosts
	out, err := s.runFunc(ctx, "-sn", "-PR", "--open", target)
	if err != nil {
		return nil, fmt.Errorf("nmap: %w", err)
	}
	hosts := parseNmapOutput(out)

	// Second pass: probe each discovered host for camera-relevant
	// ports. nmap's per-target scan is parallel and bounded by the
	// remaining ctx deadline.
	if len(hosts) > 0 {
		ips := make([]string, 0, len(hosts))
		for _, h := range hosts {
			ips = append(ips, h.IP)
		}
		ports, err := s.scanPorts(ctx, ips)
		if err == nil { // a port-probe failure is non-fatal; we keep the discovered hosts
			for i := range hosts {
				hosts[i].OpenPorts = ports[hosts[i].IP]
			}
		}
	}
	return hosts, nil
}

// scanPorts runs `nmap -p 80,443,554,8000,8080 --open -oG -` against
// the supplied IPs and returns a map of ip → []openPorts.
func (s *nmapScanner) scanPorts(ctx context.Context, ips []string) (map[string][]int, error) {
	args := append([]string{"-p", "80,443,554,8000,8080", "--open", "-oG", "-"}, ips...)
	out, err := s.runFunc(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("nmap port scan: %w", err)
	}
	return parseNmapGreppablePorts(out), nil
}

// runNmap is the production shell-out. Tests substitute a fake into
// nmapScanner.runFunc and never touch this path.
func runNmap(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "nmap", args...)
	return cmd.Output()
}

// nmapHostRE matches a typical -sn output stanza head:
//   Nmap scan report for 192.168.1.42
//   Nmap scan report for somename (192.168.1.42)
var nmapHostRE = regexp.MustCompile(`Nmap scan report for (?:[^\s]+ )?\(?([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)\)?`)

// nmapMACRE matches the MAC address line that follows a host stanza on
// LANs where ARP is available:
//   MAC Address: 44:19:B6:AA:BB:CC (Hikvision Digital Technology)
var nmapMACRE = regexp.MustCompile(`MAC Address: ([0-9A-Fa-f:]{17})`)

// parseNmapOutput walks the -sn output, pairing each "Nmap scan report"
// line with the MAC line that follows it (if any). Hosts without a MAC
// (rare on a LAN; typically the scanning host's own IP) are still
// emitted — vendor lookup will return "" on an empty MAC.
func parseNmapOutput(out []byte) []networkscan.RawHost {
	lines := strings.Split(string(out), "\n")
	hosts := make([]networkscan.RawHost, 0)
	for i, line := range lines {
		m := nmapHostRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		h := networkscan.RawHost{IP: m[1]}
		// Look ahead a few lines for the MAC.
		for j := i + 1; j < i+5 && j < len(lines); j++ {
			if mm := nmapMACRE.FindStringSubmatch(lines[j]); mm != nil {
				h.MAC = mm[1]
				break
			}
			if nmapHostRE.MatchString(lines[j]) {
				break // next host stanza started
			}
		}
		hosts = append(hosts, h)
	}
	return hosts
}

// parseNmapGreppablePorts walks `-oG -` output of the form:
//   Host: 192.168.1.42 ()	Ports: 80/open/tcp//http///, 554/open/tcp//rtsp///	...
// and returns a map ip → ascending port list.
func parseNmapGreppablePorts(out []byte) map[string][]int {
	portRE := regexp.MustCompile(`(\d+)/open/`)
	hostRE := regexp.MustCompile(`Host: (\d+\.\d+\.\d+\.\d+)`)
	result := make(map[string][]int)
	for _, line := range strings.Split(string(out), "\n") {
		hm := hostRE.FindStringSubmatch(line)
		if hm == nil {
			continue
		}
		ip := hm[1]
		seen := map[int]bool{}
		for _, pm := range portRE.FindAllStringSubmatch(line, -1) {
			p, err := strconv.Atoi(pm[1])
			if err != nil || seen[p] {
				continue
			}
			seen[p] = true
			result[ip] = append(result[ip], p)
		}
	}
	return result
}

// detectPrimarySubnet returns the /24 covering the agent's primary
// non-loopback IPv4 address. Auto-detect mode (empty CIDR override).
//
// Heuristic: pick the first up, non-loopback interface with an IPv4
// address in the private RFC1918 range. macOS install scripts have
// historically used `ipconfig getifaddr en0` for the same purpose.
func detectPrimarySubnet() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if subnet, ok := subnetCandidate(ipnet); ok {
				return subnet, nil
			}
		}
	}
	return "", errors.New("no suitable IPv4 interface")
}

// subnetCandidate returns the /24 CIDR for an *net.IPNet whose IP is
// a private RFC1918 IPv4 address, or "", false otherwise. macOS's
// iface.Addrs() returns IPv4 addresses as 16-byte IPv4-mapped slices;
// net.IP.To4 normalises both 4- and 16-byte shapes to 4 bytes, so the
// caller doesn't have to care about the OS.
//
// /24 is forced because store LANs are uniformly /24, /23, or smaller
// (going wider lengthens the scan past the 30s budget).
func subnetCandidate(ipnet *net.IPNet) (string, bool) {
	ip4 := ipnet.IP.To4()
	if ip4 == nil {
		return "", false
	}
	var a4 [4]byte
	copy(a4[:], ip4)
	if !isPrivateV4(a4) {
		return "", false
	}
	return fmt.Sprintf("%d.%d.%d.0/24", a4[0], a4[1], a4[2]), true
}

func isPrivateV4(a [4]byte) bool {
	switch {
	case a[0] == 10:
		return true
	case a[0] == 172 && a[1] >= 16 && a[1] <= 31:
		return true
	case a[0] == 192 && a[1] == 168:
		return true
	}
	return false
}
