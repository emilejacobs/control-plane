package agent

import (
	"context"
	"net"
	"testing"
)

// Bench-Mac shape end-to-end: when all three fields detect, the
// collector emits lan_ip, tailscale_ip, tailscale_name in its map.
// The publisher merges those into the heartbeat payload.
func TestNetworkCollector_AllThreeFieldsPresent(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "lo0", addrs: []string{"127.0.0.1/8"}},
		{name: "utun3", addrs: []string{"100.122.190.107/32"}},
		{name: "en0", addrs: []string{"192.168.54.215/24"}},
	}}
	runner := fakeRunner{out: []byte(`{"Self":{"DNSName":"07-store54.tailnet.ts.net."}}`)}
	c := NewNetworkCollector(enum, runner)

	got := c()
	if got["lan_ip"] != "192.168.54.215" {
		t.Errorf("lan_ip: got %v want 192.168.54.215", got["lan_ip"])
	}
	if got["tailscale_ip"] != "100.122.190.107" {
		t.Errorf("tailscale_ip: got %v want 100.122.190.107", got["tailscale_ip"])
	}
	if got["tailscale_name"] != "07-store54.tailnet.ts.net" {
		t.Errorf("tailscale_name: got %v want 07-store54.tailnet.ts.net", got["tailscale_name"])
	}
}

// Pre-tailnet device (Tailscale not installed, no utun*): the
// collector must NOT emit the tailscale_* fields. lan_ip is the
// only key present — cp-ingest's conditional UPDATE leaves the
// stored tailscale_* values untouched.
func TestNetworkCollector_OmitsAbsentFields(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "en0", addrs: []string{"192.168.1.100/24"}},
	}}
	runner := fakeRunner{err: errMissingBinary}
	c := NewNetworkCollector(enum, runner)

	got := c()
	if got["lan_ip"] != "192.168.1.100" {
		t.Errorf("lan_ip: got %v want 192.168.1.100", got["lan_ip"])
	}
	if _, present := got["tailscale_ip"]; present {
		t.Errorf("tailscale_ip should be absent when not detected; got %v", got["tailscale_ip"])
	}
	if _, present := got["tailscale_name"]; present {
		t.Errorf("tailscale_name should be absent when not detected; got %v", got["tailscale_name"])
	}
}

// Fully empty case — loopback-only host with no tailnet: the
// collector returns an empty (or nil) map. The publisher merges
// it cleanly without polluting the payload with empty-string
// values.
func TestNetworkCollector_NoDetectionEmitsNothing(t *testing.T) {
	enum := &fakeAddrEnum{ifaces: []fakeIfaceAddrs{
		{name: "lo0", addrs: []string{"127.0.0.1/8"}},
	}}
	runner := fakeRunner{out: []byte(`{}`)}
	c := NewNetworkCollector(enum, runner)

	got := c()
	if v, present := got["lan_ip"]; present {
		t.Errorf("lan_ip should be absent on loopback-only host; got %v", v)
	}
	if v, present := got["tailscale_ip"]; present {
		t.Errorf("tailscale_ip should be absent; got %v", v)
	}
	if v, present := got["tailscale_name"]; present {
		t.Errorf("tailscale_name should be absent; got %v", v)
	}
}

// errMissingBinary stands in for exec's "no such file" — the
// resolver must treat it as "", nil per cycle 2's contract, and
// the collector must not emit a tailscale_name key.
var errMissingBinary = &net.OpError{Op: "lookup", Net: "tailscale", Err: errFake("not found")}

type errFake string

func (e errFake) Error() string { return string(e) }

// Smoke: a runner that returns a context-cancelled error must not
// blow up the collector. The collector wraps the resolver call so
// any ctx wiring through Background is internal.
func TestNetworkCollector_ContextHandling(t *testing.T) {
	enum := &fakeAddrEnum{}
	runner := fakeRunner{err: context.Canceled}
	c := NewNetworkCollector(enum, runner)

	_ = c() // no panic = pass
}
