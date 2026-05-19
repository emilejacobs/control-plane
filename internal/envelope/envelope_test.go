package envelope

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCommandRoundtripsThroughJSON(t *testing.T) {
	original := Command{
		CorrelationID: "01919b73-5b0e-7000-8000-000000000001",
		CommandID:     "01919b73-5b0e-7000-8000-000000000002",
		Type:          "heartbeat",
		IssuedAt:      time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Command
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("roundtrip lost data:\noriginal: %+v\ndecoded:  %+v", original, decoded)
	}
	t.Fatal("DELIBERATE FAILURE: CI gate demo — this PR must be blocked by the ruleset")
}
