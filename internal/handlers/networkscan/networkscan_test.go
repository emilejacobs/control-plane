package networkscan_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/emilejacobs/control-plane/internal/envelope"
	handler "github.com/emilejacobs/control-plane/internal/handlers/networkscan"
	"github.com/emilejacobs/control-plane/internal/protocol/networkscan"
)

// expectCodedError asserts err is an *envelope.CodedError with the
// given stable code. Mirrors the cameras / logtail handler tests.
func expectCodedError(t *testing.T, err error, code string) {
	t.Helper()
	var coded *envelope.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error not *envelope.CodedError: %v (type %T)", err, err)
	}
	if coded.Code != code {
		t.Errorf("code: got %q, want %q", coded.Code, code)
	}
}

// fakeScanner returns canned hosts + lets tests inject an error /
// observe the CIDR the handler passed through.
type fakeScanner struct {
	hosts []handler.RawHost
	err   error
	calls []string // captured CIDR per call
}

func (f *fakeScanner) Scan(_ context.Context, cidr string) ([]handler.RawHost, error) {
	f.calls = append(f.calls, cidr)
	if f.err != nil {
		return nil, f.err
	}
	return f.hosts, nil
}

// Happy path: payload with a cidr override is parsed → scanner is
// called with that CIDR → hosts come back, vendor is resolved via the
// OUI table, open_ports is filtered to the camera-relevant set and
// sorted ascending.
func TestNetworkScanHappyPath(t *testing.T) {
	sc := &fakeScanner{
		hosts: []handler.RawHost{
			{
				IP:        "192.168.1.10",
				MAC:       "44:19:B6:AA:BB:CC", // Hikvision
				OpenPorts: []int{554, 80, 22, 8080},
			},
			{
				IP:        "192.168.1.42",
				MAC:       "3c:ef:8c:11:22:33", // Dahua
				OpenPorts: []int{443},
			},
		},
	}
	h := handler.New(sc)

	raw := json.RawMessage(`{"cidr":"192.168.1.0/24"}`)
	resp, err := h.Handle(context.Background(), raw)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	r, ok := resp.(networkscan.Response)
	if !ok {
		t.Fatalf("response type: got %T, want networkscan.Response", resp)
	}
	if len(sc.calls) != 1 || sc.calls[0] != "192.168.1.0/24" {
		t.Errorf("Scanner.Scan calls: got %+v, want one call with 192.168.1.0/24", sc.calls)
	}
	if len(r.Hosts) != 2 {
		t.Fatalf("hosts: got %d, want 2", len(r.Hosts))
	}
	// MAC is canonicalised to lowercase.
	if r.Hosts[0].MAC != "44:19:b6:aa:bb:cc" {
		t.Errorf("MAC canonicalisation: got %q, want lowercase", r.Hosts[0].MAC)
	}
	if r.Hosts[0].Vendor != "Hikvision" {
		t.Errorf("vendor[0]: got %q, want Hikvision", r.Hosts[0].Vendor)
	}
	if r.Hosts[1].Vendor != "Dahua" {
		t.Errorf("vendor[1]: got %q, want Dahua", r.Hosts[1].Vendor)
	}
	// open_ports: 22 was dropped (not camera-relevant); the rest are
	// sorted ascending.
	wantPorts := []int{80, 554, 8080}
	if len(r.Hosts[0].OpenPorts) != len(wantPorts) {
		t.Fatalf("ports[0]: got %v, want %v", r.Hosts[0].OpenPorts, wantPorts)
	}
	for i, p := range wantPorts {
		if r.Hosts[0].OpenPorts[i] != p {
			t.Errorf("ports[0][%d]: got %d, want %d", i, r.Hosts[0].OpenPorts[i], p)
		}
	}
}

// Empty payload (auto-detect mode) — the scanner is called with the
// empty string; the handler does not error.
func TestNetworkScanAutoDetect(t *testing.T) {
	sc := &fakeScanner{hosts: []handler.RawHost{}}
	h := handler.New(sc)

	resp, err := h.Handle(context.Background(), json.RawMessage(``))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	r := resp.(networkscan.Response)
	if r.Hosts == nil {
		t.Error("Hosts should be non-nil (an empty slice) for empty scan")
	}
	if len(sc.calls) != 1 || sc.calls[0] != "" {
		t.Errorf("scanner calls: got %+v, want [\"\"]", sc.calls)
	}
}

// Unknown top-level field is rejected with the protocol's
// CodeUnknownField — the scanner is not called.
func TestNetworkScanRejectsUnknownField(t *testing.T) {
	sc := &fakeScanner{}
	h := handler.New(sc)

	raw := json.RawMessage(`{"cidr":"10.0.0.0/24","extra":"hi"}`)
	_, err := h.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectCodedError(t, err, networkscan.CodeUnknownField)
	if len(sc.calls) != 0 {
		t.Errorf("Scanner.Scan must not be called on validation failure; got %d", len(sc.calls))
	}
}

// Bad CIDR surfaces with the CodeBadCIDR code; scanner is not called.
func TestNetworkScanRejectsBadCIDR(t *testing.T) {
	sc := &fakeScanner{}
	h := handler.New(sc)

	_, err := h.Handle(context.Background(), json.RawMessage(`{"cidr":"nope"}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectCodedError(t, err, networkscan.CodeBadCIDR)
	if len(sc.calls) != 0 {
		t.Errorf("Scanner must not be called on validation failure; got %d", len(sc.calls))
	}
}

// Scanner failure: handler wraps the underlying error in a
// CodeScanFailed envelope error so the operator sees a stable code on
// the dashboard.
func TestNetworkScanScannerError(t *testing.T) {
	sc := &fakeScanner{err: errors.New("arp-scan exit 2")}
	h := handler.New(sc)

	_, err := h.Handle(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectCodedError(t, err, networkscan.CodeScanFailed)
}
