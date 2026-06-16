package snapshotconfig_test

import (
	"encoding/json"
	"testing"

	"github.com/emilejacobs/control-plane/internal/protocol/snapshotconfig"
)

func TestParseArgs(t *testing.T) {
	for _, c := range []string{"off", "daily", "weekly"} {
		a, err := snapshotconfig.ParseArgs(json.RawMessage(`{"cadence":"` + c + `"}`))
		if err != nil {
			t.Errorf("cadence %q: %v", c, err)
		}
		if a.Cadence != c {
			t.Errorf("parsed cadence = %q, want %q", a.Cadence, c)
		}
	}
}

func TestParseArgsRejects(t *testing.T) {
	cases := map[string]string{
		"unknown cadence": `{"cadence":"hourly"}`,
		"empty cadence":   `{"cadence":""}`,
		"unknown field":   `{"cadence":"weekly","extra":1}`,
		"not an object":   `"weekly"`,
	}
	for name, raw := range cases {
		if _, err := snapshotconfig.ParseArgs(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
