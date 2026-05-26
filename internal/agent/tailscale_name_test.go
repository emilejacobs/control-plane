package agent

import (
	"context"
	"errors"
	"testing"
)

// fakeRunner implements TailscaleStatusRunner for tests. Either out
// or err is non-nil per call — same shape as exec.Cmd.Output().
type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Status(_ context.Context) ([]byte, error) {
	return f.out, f.err
}

// Bench-Mac case: `tailscale status --json` returns the device's
// MagicDNS name with the trailing dot the API includes. The
// resolver strips the dot so the dashboard's URL builder doesn't
// emit "07-foo.tailnet.ts.net.:5051".
func TestResolveTailscaleName_StripsTrailingDot(t *testing.T) {
	r := fakeRunner{out: []byte(`{
		"Self": {
			"DNSName": "07-eegees-store54-macmini.tailnet.ts.net."
		}
	}`)}
	got, err := ResolveTailscaleName(context.Background(), r)
	if err != nil {
		t.Fatalf("ResolveTailscaleName: %v", err)
	}
	if got != "07-eegees-store54-macmini.tailnet.ts.net" {
		t.Errorf("name: got %q want 07-eegees-store54-macmini.tailnet.ts.net", got)
	}
}

// Missing binary: dev box / non-tailnet device. Resolver must
// return "", nil — never propagate an error that would fail the
// heartbeat.
func TestResolveTailscaleName_MissingBinary_ReturnsEmptyNil(t *testing.T) {
	r := fakeRunner{err: errors.New("exec: \"tailscale\": executable file not found in $PATH")}
	got, err := ResolveTailscaleName(context.Background(), r)
	if err != nil {
		t.Errorf("ResolveTailscaleName missing-binary: got err %v want nil", err)
	}
	if got != "" {
		t.Errorf("name: got %q want \"\"", got)
	}
}

// Logged-out / non-tailnet device: `tailscale status --json`
// returns valid JSON with empty DNSName (or sometimes an entirely
// different shape). Treat as null.
func TestResolveTailscaleName_EmptyDNSName_ReturnsEmptyNil(t *testing.T) {
	r := fakeRunner{out: []byte(`{"Self": {"DNSName": ""}}`)}
	got, err := ResolveTailscaleName(context.Background(), r)
	if err != nil {
		t.Errorf("ResolveTailscaleName empty-dns: got err %v want nil", err)
	}
	if got != "" {
		t.Errorf("name: got %q want \"\"", got)
	}
}

// Malformed JSON: never fail the heartbeat.
func TestResolveTailscaleName_MalformedJSON_ReturnsEmptyNil(t *testing.T) {
	r := fakeRunner{out: []byte(`{this is not json`)}
	got, err := ResolveTailscaleName(context.Background(), r)
	if err != nil {
		t.Errorf("ResolveTailscaleName malformed: got err %v want nil", err)
	}
	if got != "" {
		t.Errorf("name: got %q want \"\"", got)
	}
}

// "Logged out" shape: tailscaled responds with valid JSON but
// without a Self block. Treat as null.
func TestResolveTailscaleName_NoSelf_ReturnsEmptyNil(t *testing.T) {
	r := fakeRunner{out: []byte(`{"BackendState": "Stopped"}`)}
	got, err := ResolveTailscaleName(context.Background(), r)
	if err != nil {
		t.Errorf("ResolveTailscaleName no-self: got err %v want nil", err)
	}
	if got != "" {
		t.Errorf("name: got %q want \"\"", got)
	}
}
