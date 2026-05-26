// Package networkscan implements the agent-side handler for the
// downward `network.scan` command (Phase 2 Edge UI rework, issue #3,
// ADR-030 § 2). Wire types + validation live in
// internal/protocol/networkscan so the agent and CP halves can't drift
// on what a valid request — or a valid result row — looks like.
//
// `network.scan` is the SIXTH unsigned dispatcher handler (per ADR-028
// extended; ADR-030 § 2 enumerates it). The scanner is read-only on
// the LAN; the worst an attacker who can publish to the cmd topic can
// do is force a noisy probe of the device's local subnet.
package networkscan

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
)

// cameraPorts is the curated set of TCP ports the handler keeps in the
// per-host open_ports list. Anything else the scanner finds is dropped
// — the operator only cares about camera-shaped services here. Order
// matters only for the membership test (we use a map below).
var cameraPorts = map[int]bool{
	80:   true, // HTTP control panel
	443:  true, // HTTPS control panel
	554:  true, // RTSP
	8000: true, // common alt HTTP
	8080: true, // common alt HTTP
}

// RawHost is the un-enriched per-host shape a Scanner returns. The
// handler resolves Vendor via the embedded OUI table, filters and
// sorts OpenPorts, and canonicalises MAC to lowercase before producing
// the wire-shape networkscan.Host. Keeping this as a separate type
// makes the Scanner contract narrow — implementations don't need to
// know about OUI tables or port filters.
type RawHost struct {
	IP        string
	MAC       string
	OpenPorts []int
}

// Scanner is the LAN-scan side of the agent the handler depends on.
// Implementations may shell out to arp-scan / nmap, parse netstat-ish
// output, or generate fakes for tests. cidr=="" means "auto-detect the
// device's primary subnet"; the Scanner picks the interface.
//
// Context cancellation must terminate the scan promptly (acceptance
// criterion: 30s ceiling). The agent dispatcher will cancel on agent
// shutdown.
type Scanner interface {
	Scan(ctx context.Context, cidr string) ([]RawHost, error)
}

type Handler struct {
	scanner Scanner
}

func New(scanner Scanner) *Handler {
	return &Handler{scanner: scanner}
}

// Handle parses + validates the network.scan payload, invokes the
// scanner, and enriches the raw host list (OUI lookup, port filter,
// MAC canonicalisation) before returning the wire-shape Response.
func (h *Handler) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	req, err := networkscan.ParseRequest(args)
	if err != nil {
		if v, ok := networkscan.AsValidation(err); ok {
			return nil, envelope.NewCodedError(v.Code, v.Message)
		}
		return nil, envelope.NewCodedError(networkscan.CodeBadPayload, err.Error())
	}

	raw, err := h.scanner.Scan(ctx, req.CIDR)
	if err != nil {
		return nil, envelope.NewCodedError(networkscan.CodeScanFailed, err.Error())
	}

	hosts := make([]networkscan.Host, 0, len(raw))
	for _, r := range raw {
		mac := strings.ToLower(r.MAC)
		ports := filterCameraPorts(r.OpenPorts)
		hosts = append(hosts, networkscan.Host{
			IP:        r.IP,
			MAC:       mac,
			Vendor:    LookupVendor(mac),
			OpenPorts: ports,
		})
	}
	return networkscan.Response{Hosts: hosts}, nil
}

// filterCameraPorts keeps only the camera-relevant ports and returns
// them sorted ascending. Always returns a non-nil slice (empty when
// nothing matched) so the wire shape is "open_ports":[].
func filterCameraPorts(in []int) []int {
	out := make([]int, 0, len(in))
	for _, p := range in {
		if cameraPorts[p] {
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}
