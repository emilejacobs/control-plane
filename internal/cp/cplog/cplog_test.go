package cplog_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
)

func TestNewEmitsStandardADR011Fields(t *testing.T) {
	var buf bytes.Buffer
	logger := cplog.New(&buf, "cp-api")

	logger.Info("hello")

	var line map[string]any
	if err := json.NewDecoder(&buf).Decode(&line); err != nil {
		t.Fatalf("decode log line: %v", err)
	}

	if got := line["service"]; got != "cp-api" {
		t.Errorf("service: got %v want %q", got, "cp-api")
	}
	if got := line["msg"]; got != "hello" {
		t.Errorf("msg: got %v want %q", got, "hello")
	}
	if got := line["level"]; got != "INFO" {
		t.Errorf("level: got %v want INFO", got)
	}
	// ADR-011 names the timestamp field `ts`, not slog's default `time`.
	if _, ok := line["ts"]; !ok {
		t.Errorf("ts field missing; got keys=%v", keys(line))
	}
	if _, ok := line["time"]; ok {
		t.Errorf("time field present; ADR-011 wants `ts` instead")
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
